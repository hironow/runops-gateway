package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultPendingApprovalsCollection is the Firestore collection name used
// by FirestorePendingStore when no override is provided. Stored documents
// represent in-flight 4-eyes approval requests for HIGH severity admin
// endpoint mutations (ADR 0039 §async two-phase + Firestore pending state).
const DefaultPendingApprovalsCollection = "pending_approvals"

// FirestorePendingStore persists PendingApproval records in a Firestore
// collection. It is the production adapter for ADR 0039 and is multi-
// instance safe: CreateIfNotExists uses Doc.Create for the
// codes.AlreadyExists semantics, mirroring FirestoreProjectRegistry's
// idempotent insert pattern. Transition uses a Firestore RunTransaction so
// that terminal-state validation and the Update happen atomically; without
// a transaction two parallel approvers could both observe a non-terminal
// state and double-write.
type FirestorePendingStore struct {
	client     *firestore.Client
	collection string
}

// NewFirestorePendingStore wires a Firestore client into the PendingStore
// port. Caller retains ownership of the client.
func NewFirestorePendingStore(client *firestore.Client, collection string) *FirestorePendingStore {
	if collection == "" {
		collection = DefaultPendingApprovalsCollection
	}
	return &FirestorePendingStore{client: client, collection: collection}
}

// CreateIfNotExists inserts a new PendingApproval keyed by IdempotencyKey.
// If a document already exists we read it back and return ErrPendingAlreadyExists
// alongside the existing record so the caller can present the prior state
// to the operator (= idempotent retry on the same body).
func (s *FirestorePendingStore) CreateIfNotExists(
	ctx context.Context,
	p domain.PendingApproval,
) (domain.PendingApproval, error) {
	if p.IdempotencyKey == "" {
		return domain.PendingApproval{}, errors.New("idempotency key required")
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = nowUTC()
	}
	if p.Status == "" {
		p.Status = domain.PendingStatusPendingApproval
	}

	doc := s.client.Collection(s.collection).Doc(p.IdempotencyKey)
	if _, err := doc.Create(ctx, p); err != nil {
		if status.Code(err) == codes.AlreadyExists {
			existing, getErr := s.Get(ctx, p.IdempotencyKey)
			if getErr != nil {
				return domain.PendingApproval{},
					fmt.Errorf("firestore create-if-not-exists fetch existing: %w", getErr)
			}
			return existing, port.ErrPendingAlreadyExists
		}
		return domain.PendingApproval{}, fmt.Errorf("firestore create: %w", err)
	}
	return p, nil
}

// Get fetches a PendingApproval by its IdempotencyKey. Missing documents
// return ErrPendingNotFound rather than the underlying gRPC code so callers
// can use the shared sentinel.
func (s *FirestorePendingStore) Get(
	ctx context.Context,
	idempotencyKey string,
) (domain.PendingApproval, error) {
	snap, err := s.client.Collection(s.collection).Doc(idempotencyKey).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.PendingApproval{}, port.ErrPendingNotFound
		}
		return domain.PendingApproval{}, fmt.Errorf("firestore get: %w", err)
	}
	var p domain.PendingApproval
	if err := snap.DataTo(&p); err != nil {
		return domain.PendingApproval{}, fmt.Errorf("decode pending approval: %w", err)
	}
	return p, nil
}

// Transition updates the PendingApproval status and (when applicable)
// AppliedAt. Validation rules:
//
//   - newStatus = PendingStatusApprovedApplied requires non-nil appliedAt
//   - newStatus != PendingStatusApprovedApplied requires nil appliedAt
//   - existing record at terminal state returns ErrPendingTerminalTransition
//   - missing record returns ErrPendingNotFound
//
// Read + write are wrapped in a Firestore RunTransaction so the terminal
// check and the Update commit atomically.
func (s *FirestorePendingStore) Transition(
	ctx context.Context,
	idempotencyKey string,
	newStatus domain.PendingStatus,
	appliedAt *time.Time,
) error {
	if newStatus == domain.PendingStatusApprovedApplied && appliedAt == nil {
		return errors.New("approved_applied transition requires non-nil appliedAt")
	}
	if newStatus != domain.PendingStatusApprovedApplied && appliedAt != nil {
		return errors.New("non-approved transition must not pass appliedAt")
	}

	doc := s.client.Collection(s.collection).Doc(idempotencyKey)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(doc)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return port.ErrPendingNotFound
			}
			return fmt.Errorf("firestore tx get: %w", err)
		}
		var existing domain.PendingApproval
		if err := snap.DataTo(&existing); err != nil {
			return fmt.Errorf("decode existing for transition: %w", err)
		}
		if existing.Status.IsTerminal() {
			return port.ErrPendingTerminalTransition
		}
		updates := []firestore.Update{
			{Path: "status", Value: string(newStatus)},
		}
		if appliedAt != nil {
			updates = append(updates, firestore.Update{Path: "applied_at", Value: *appliedAt})
		}
		if err := tx.Update(doc, updates); err != nil {
			return fmt.Errorf("firestore tx update: %w", err)
		}
		return nil
	})
}
