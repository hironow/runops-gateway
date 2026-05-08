package registry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// DefaultAgentSessionsCollection is the Firestore collection name
// used by FirestoreAgentSessionRegistry when no override is provided.
const DefaultAgentSessionsCollection = "broker_agent_sessions"

// FirestoreAgentSessionRegistry persists AI-agent sessions in a
// Firestore collection. Multi-instance safe (Firestore manages
// persistence + concurrency); satisfies the Cloud Run multi-instance
// contract documented on the AgentSessionRegistry port.
//
// Production deployments select this adapter via
// BROKER_USE_FIRESTORE_REGISTRY=true (Phase 3b-3a env var). The
// in-memory variant in this same package remains the default for
// dev / staging / single-instance Cloud Run.
type FirestoreAgentSessionRegistry struct {
	client     *firestore.Client
	collection string
}

// NewFirestoreAgentSessionRegistry wires a Firestore client into
// the AgentSessionRegistry port. The caller retains ownership of
// the client and is responsible for Close() at shutdown.
func NewFirestoreAgentSessionRegistry(client *firestore.Client, collection string) (*FirestoreAgentSessionRegistry, error) {
	if client == nil {
		return nil, ErrFirestoreNilClient
	}
	if collection == "" {
		collection = DefaultAgentSessionsCollection
	}
	return &FirestoreAgentSessionRegistry{client: client, collection: collection}, nil
}

// Register stores sess as a new document keyed on SessionID. Returns
// an error wrapping codes.AlreadyExists when the SessionID is already
// registered (= matches the in-memory registry's "duplicate rejected"
// contract from Phase 2c-2-1).
func (r *FirestoreAgentSessionRegistry) Register(ctx context.Context, sess domain.AgentSession) error {
	if sess.SessionID == "" {
		return fmt.Errorf("firestore_agent_session: SessionID is required")
	}
	_, err := r.client.Collection(r.collection).Doc(sess.SessionID).Create(ctx, sess)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return fmt.Errorf("firestore_agent_session: SessionID %q already registered", sess.SessionID)
		}
		return fmt.Errorf("firestore_agent_session: Create %q: %w", sess.SessionID, err)
	}
	return nil
}

// Get returns the AgentSession for sessionID. Returns
// domain.ErrAgentSessionNotFound when the document does not exist
// (codes.NotFound at the Firestore layer).
func (r *FirestoreAgentSessionRegistry) Get(ctx context.Context, sessionID string) (domain.AgentSession, error) {
	doc, err := r.client.Collection(r.collection).Doc(sessionID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.AgentSession{}, domain.ErrAgentSessionNotFound
		}
		return domain.AgentSession{}, fmt.Errorf("firestore_agent_session: Get %q: %w", sessionID, err)
	}
	var sess domain.AgentSession
	if err := doc.DataTo(&sess); err != nil {
		return domain.AgentSession{}, fmt.Errorf("firestore_agent_session: DataTo %q: %w", sessionID, err)
	}
	return sess, nil
}

// Revoke marks the session as revoked by setting the revoked_at
// field. Idempotent: the second call rewrites the same field to
// the same value (Firestore Update is upsert-style on individual
// fields), so the call succeeds silently. Returns
// domain.ErrAgentSessionNotFound when the document does not exist.
func (r *FirestoreAgentSessionRegistry) Revoke(ctx context.Context, sessionID string) error {
	now := time.Now().UTC()
	doc := r.client.Collection(r.collection).Doc(sessionID)

	// Pre-check existence so we can surface ErrAgentSessionNotFound
	// without depending on Update's error code (which has historically
	// varied between gRPC versions for "doc missing").
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.ErrAgentSessionNotFound
		}
		return fmt.Errorf("firestore_agent_session: Revoke pre-check %q: %w", sessionID, err)
	}

	// Idempotent: if revoked_at is already set, leave the original
	// timestamp in place — Phase 2c-1's contract says the field is
	// the audit timestamp, not a future-effective revocation date.
	var existing domain.AgentSession
	if err := snap.DataTo(&existing); err == nil && existing.RevokedAt != nil {
		return nil
	}

	_, err = doc.Update(ctx, []firestore.Update{
		{Path: "revoked_at", Value: now},
	})
	if err != nil {
		return fmt.Errorf("firestore_agent_session: Revoke update %q: %w", sessionID, err)
	}
	return nil
}

// Sentinel errors raised by NewFirestoreAgentSessionRegistry.
var (
	ErrFirestoreNilClient = errors.New("firestore_agent_session: client must be non-nil")
)
