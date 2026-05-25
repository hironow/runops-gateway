package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hironow/runops-gateway/internal/core/port"
)

// envPendingCollection overrides the default Firestore collection for
// pending approvals. Optional; defaults to DefaultPendingCollection.
const envPendingCollection = "RUNOPS_PENDING_COLLECTION"

// DefaultPendingCollection is the Firestore collection name for
// PendingApproval documents.
const DefaultPendingCollection = "pending_approvals"

// ResolvePendingStoreFromEnv constructs a PendingStore mirroring the env
// contract used by ResolveFromEnv:
//
//   - RUNOPS_PROJECT_REGISTRY=sqlite|firestore selects the backend
//   - RUNOPS_STATE_DB_PATH for sqlite (= same DB file as the registry, OK
//     because SQLite handles concurrent connections under WAL mode)
//   - GOOGLE_CLOUD_PROJECT + RUNOPS_FIRESTORE_DATABASE for firestore
//   - RUNOPS_PENDING_COLLECTION overrides "pending_approvals"
//
// Returns (nil, no-op cleanup, nil) when no registry env is set so the
// caller can treat the pending store as optional (= /rpc endpoint stays
// disabled when neither registry nor flag is configured).
//
// This resolver opens a SEPARATE handle (= second SQLite connection or
// second Firestore client) from the project registry resolver. That is
// acceptable for §B-4 because the /rpc endpoint is opt-in; §B-5 will
// revisit shared-handle reuse once the orchestrator joins the topology.
func ResolvePendingStoreFromEnv(ctx context.Context) (port.PendingStore, CleanupFunc, error) {
	if os.Getenv(envProjectRegistry) == "" && os.Getenv(envRunopsEnv) != "development" {
		return nil, noopCleanup, nil
	}
	return newPendingStoreFromEnv(ctx, os.Getenv)
}

// newPendingStoreFromEnv is the testable inner.
func newPendingStoreFromEnv(ctx context.Context, getenv func(string) string) (port.PendingStore, CleanupFunc, error) {
	choice := getenv(envProjectRegistry)
	if choice == "" {
		if getenv(envRunopsEnv) == "development" {
			choice = "sqlite"
		} else {
			return nil, noopCleanup, fmt.Errorf("%s env required for pending store", envProjectRegistry)
		}
	}

	switch choice {
	case "sqlite":
		return newSQLitePendingStoreFromEnv(ctx, getenv)
	case "firestore":
		return newFirestorePendingStoreFromEnv(ctx, getenv)
	default:
		return nil, noopCleanup, fmt.Errorf("unknown %s value: %q (want sqlite or firestore)", envProjectRegistry, choice)
	}
}

func newSQLitePendingStoreFromEnv(ctx context.Context, getenv func(string) string) (port.PendingStore, CleanupFunc, error) {
	dbPath := getenv(envStateDBPath)
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, noopCleanup, fmt.Errorf("resolve home dir for default %s: %w", envStateDBPath, err)
		}
		dbPath = filepath.Join(home, ".runops", "state.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, noopCleanup, fmt.Errorf("create state dir: %w", err)
	}
	db, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		return nil, noopCleanup, fmt.Errorf("open sqlite for pending store: %w", err)
	}
	return NewSQLitePendingStore(db), db.Close, nil
}

func newFirestorePendingStoreFromEnv(ctx context.Context, getenv func(string) string) (port.PendingStore, CleanupFunc, error) {
	projectID := getenv(envGoogleCloudProject)
	if projectID == "" {
		return nil, noopCleanup, fmt.Errorf("%s env required for firestore pending store", envGoogleCloudProject)
	}
	dbName := getenv(envFirestoreDatabase)
	collection := getenv(envPendingCollection)
	if collection == "" {
		collection = DefaultPendingCollection
	}

	client, err := newFirestoreClient(ctx, projectID, dbName)
	if err != nil {
		return nil, noopCleanup, fmt.Errorf("firestore client for pending store: %w", err)
	}
	return NewFirestorePendingStore(client, collection), client.Close, nil
}
