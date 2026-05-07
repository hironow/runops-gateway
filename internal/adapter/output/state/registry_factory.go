package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hironow/runops-gateway/internal/core/port"
)

// Env var names recognized by NewProjectRegistryFromEnv.
const (
	envProjectRegistry = "RUNOPS_PROJECT_REGISTRY"
	envRunopsEnv       = "RUNOPS_ENV"
	envStateDBPath     = "RUNOPS_STATE_DB_PATH"
)

// CleanupFunc releases resources held by a ProjectRegistry adapter
// (e.g. SQLite *sql.DB or Firestore client). Composition roots MUST defer
// the returned cleanup so long-running services do not leak file
// descriptors or gRPC connections.
type CleanupFunc func() error

// noopCleanup is a stable cleanup returned by error paths so callers can
// always defer the result without nil checks.
func noopCleanup() error { return nil }

// NewProjectRegistryFromEnv selects and constructs a ProjectRegistry based
// on env vars. The factory is fail-closed: env must be explicit, with a
// single dev-mode escape hatch (RUNOPS_ENV=development) that defaults to
// sqlite for developer ergonomics.
//
//	RUNOPS_PROJECT_REGISTRY=sqlite     SQLite adapter (issue #0009)
//	RUNOPS_PROJECT_REGISTRY=firestore  Firestore adapter (issue #0011)
//	(unset)                            error, unless RUNOPS_ENV=development
//	(other value)                      error
//
// The second return value is a cleanup function that closes the adapter's
// underlying handle. Callers MUST defer it (a no-op cleanup is returned on
// error paths so deferring is always safe).
//
// SQLite path comes from RUNOPS_STATE_DB_PATH; if unset, defaults to
// $HOME/.runops/state.db. The DB file (and parent dir) are created on first
// open.
//
// getenv is injected for testability — production callers should pass
// os.Getenv.
func NewProjectRegistryFromEnv(ctx context.Context, getenv func(string) string) (port.ProjectRegistry, CleanupFunc, error) {
	choice := getenv(envProjectRegistry)
	runopsEnv := getenv(envRunopsEnv)

	if choice == "" {
		if runopsEnv == "development" {
			choice = "sqlite"
		} else {
			return nil, noopCleanup, fmt.Errorf("%s env required (sqlite|firestore); set %s=development to default to sqlite for local development",
				envProjectRegistry, envRunopsEnv)
		}
	}

	switch choice {
	case "sqlite":
		return newSQLiteRegistryFromEnv(ctx, getenv)
	case "firestore":
		return nil, noopCleanup, fmt.Errorf("firestore adapter not implemented yet, see issue #0011")
	default:
		return nil, noopCleanup, fmt.Errorf("unknown %s value: %q (want sqlite or firestore)", envProjectRegistry, choice)
	}
}

func newSQLiteRegistryFromEnv(ctx context.Context, getenv func(string) string) (port.ProjectRegistry, CleanupFunc, error) {
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
		return nil, noopCleanup, err
	}
	return NewSQLiteProjectRegistry(db), db.Close, nil
}
