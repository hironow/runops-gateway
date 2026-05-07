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

// NewProjectRegistryFromEnv selects and constructs a ProjectRegistry based
// on env vars. The factory is fail-closed: env must be explicit, with a
// single dev-mode escape hatch (RUNOPS_ENV=development) that defaults to
// sqlite for developer ergonomics.
//
//	RUNOPS_PROJECT_REGISTRY=sqlite     SQLite adapter (this PR)
//	RUNOPS_PROJECT_REGISTRY=firestore  reserved for #0011 (returns error)
//	(unset)                            error, unless RUNOPS_ENV=development
//	(other value)                      error
//
// SQLite path comes from RUNOPS_STATE_DB_PATH; if unset, defaults to
// $HOME/.runops/state.db. The DB file (and parent dir) are created on first
// open.
//
// getenv is injected for testability — production callers should pass
// os.Getenv.
func NewProjectRegistryFromEnv(ctx context.Context, getenv func(string) string) (port.ProjectRegistry, error) {
	choice := getenv(envProjectRegistry)
	runopsEnv := getenv(envRunopsEnv)

	if choice == "" {
		if runopsEnv == "development" {
			choice = "sqlite"
		} else {
			return nil, fmt.Errorf("%s env required (sqlite|firestore); set %s=development to default to sqlite for local development",
				envProjectRegistry, envRunopsEnv)
		}
	}

	switch choice {
	case "sqlite":
		return newSQLiteRegistryFromEnv(ctx, getenv)
	case "firestore":
		return nil, fmt.Errorf("firestore adapter not implemented yet, see issue #0011")
	default:
		return nil, fmt.Errorf("unknown %s value: %q (want sqlite or firestore)", envProjectRegistry, choice)
	}
}

func newSQLiteRegistryFromEnv(ctx context.Context, getenv func(string) string) (port.ProjectRegistry, error) {
	dbPath := getenv(envStateDBPath)
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir for default %s: %w", envStateDBPath, err)
		}
		dbPath = filepath.Join(home, ".runops", "state.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	db, err := OpenSQLite(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	return NewSQLiteProjectRegistry(db), nil
}
