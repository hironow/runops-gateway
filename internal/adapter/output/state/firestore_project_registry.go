package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultProjectsCollection is the Firestore collection name used by
// FirestoreProjectRegistry when no override is provided. It mirrors the
// SQLite `projects` table.
const DefaultProjectsCollection = "projects"

// FirestoreProjectRegistry persists projects in a Firestore collection.
//
// It is the production adapter for issue #0011 and is multi-instance safe
// (Firestore manages persistence and concurrency). For dev / test / local
// use cases see SQLiteProjectRegistry; ADR 0026 documents which adapter is
// selected per environment.
type FirestoreProjectRegistry struct {
	client     *firestore.Client
	collection string
}

// NewFirestoreProjectRegistry wires a Firestore client into the
// ProjectRegistry port. The caller retains ownership of the client and
// must Close it (the registry factory returns a CleanupFunc that does so).
func NewFirestoreProjectRegistry(client *firestore.Client, collection string) *FirestoreProjectRegistry {
	if collection == "" {
		collection = DefaultProjectsCollection
	}
	return &FirestoreProjectRegistry{client: client, collection: collection}
}

// Add inserts a project document. Firestore's Doc.Create returns
// codes.AlreadyExists when the document id is taken; we map that to the
// shared ErrProjectAlreadyExists sentinel. Invalid project ids are
// rejected before any RPC.
func (r *FirestoreProjectRegistry) Add(ctx context.Context, p domain.Project) error {
	if err := domain.ValidateProjectID(p.ID); err != nil {
		return err
	}
	if p.Status == "" {
		p.Status = domain.ProjectStatusActive
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = nowUTC()
	}
	_, err := r.client.Collection(r.collection).Doc(p.ID).Create(ctx, p)
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return domain.ErrProjectAlreadyExists
		}
		return fmt.Errorf("firestore create: %w", err)
	}
	return nil
}

// Get fetches a single project by id. Missing documents return
// ErrProjectNotFound rather than the underlying gRPC error so callers can
// rely on the same sentinel as the SQLite adapter.
func (r *FirestoreProjectRegistry) Get(ctx context.Context, id string) (domain.Project, error) {
	snap, err := r.client.Collection(r.collection).Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.Project{}, domain.ErrProjectNotFound
		}
		return domain.Project{}, fmt.Errorf("firestore get: %w", err)
	}
	var p domain.Project
	if err := snap.DataTo(&p); err != nil {
		return domain.Project{}, fmt.Errorf("decode project: %w", err)
	}
	return p, nil
}

// List returns all projects, optionally filtered by status, sorted by id
// for deterministic output (matches SQLite adapter behavior).
func (r *FirestoreProjectRegistry) List(ctx context.Context, filter port.ProjectListFilter) ([]domain.Project, error) {
	q := r.client.Collection(r.collection).Query
	if filter.Status != "" {
		q = q.Where("status", "==", string(filter.Status))
	}
	q = q.OrderBy("id", firestore.Asc)

	iter := q.Documents(ctx)
	defer iter.Stop()

	var out []domain.Project
	for {
		snap, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("firestore list: %w", err)
		}
		var p domain.Project
		if err := snap.DataTo(&p); err != nil {
			return nil, fmt.Errorf("decode project: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

// Archive marks a project as archived. To preserve idempotency (matching
// the SQLite adapter contract) we read first and skip the write when the
// project is already archived; this also keeps the original ArchivedAt
// timestamp instead of overwriting it.
func (r *FirestoreProjectRegistry) Archive(ctx context.Context, id string) error {
	doc := r.client.Collection(r.collection).Doc(id)
	snap, err := doc.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.ErrProjectNotFound
		}
		return fmt.Errorf("firestore get for archive: %w", err)
	}
	var existing domain.Project
	if err := snap.DataTo(&existing); err != nil {
		return fmt.Errorf("decode existing for archive: %w", err)
	}
	if existing.Status == domain.ProjectStatusArchived {
		return nil // idempotent: already archived, preserve ArchivedAt
	}
	now := nowUTC()
	_, err = doc.Update(ctx, []firestore.Update{
		{Path: "status", Value: string(domain.ProjectStatusArchived)},
		{Path: "archived_at", Value: now},
	})
	if err != nil {
		return fmt.Errorf("firestore archive update: %w", err)
	}
	return nil
}

// nowUTC is a small seam so tests could override time if needed; right
// now it just delegates to time.Now().UTC(). Centralized so SQLite and
// Firestore adapters agree on timezone semantics if one of them later
// wants control.
var nowUTC = func() time.Time {
	return time.Now().UTC()
}
