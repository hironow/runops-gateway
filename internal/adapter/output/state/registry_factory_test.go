package state_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
)

// envSet replaces os.Getenv for the duration of the test via a small lookup
// fn so we never touch process-wide state in parallel tests.
type envFn func(string) string

func envFromMap(m map[string]string) envFn {
	return func(key string) string { return m[key] }
}

func TestNewProjectRegistry_RejectsMissingEnv(t *testing.T) {
	// Given: no RUNOPS_PROJECT_REGISTRY, no RUNOPS_ENV.
	getenv := envFromMap(map[string]string{})

	// When.
	_, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })

	// Then: explicit fail-fast, not silent default.
	if err == nil {
		t.Fatalf("want error for missing env, got nil")
	}
	if !strings.Contains(err.Error(), "RUNOPS_PROJECT_REGISTRY") {
		t.Errorf("error should mention RUNOPS_PROJECT_REGISTRY, got %q", err.Error())
	}
}

func TestNewProjectRegistry_DevEnvDefaultsToSqlite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	getenv := envFromMap(map[string]string{
		"RUNOPS_ENV":           "development",
		"RUNOPS_STATE_DB_PATH": dbPath,
	})

	reg, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })
	if err != nil {
		t.Fatalf("dev env should default to sqlite: %v", err)
	}
	if reg == nil {
		t.Fatalf("want non-nil registry")
	}
}

func TestNewProjectRegistry_NonDevEnvRequiresExplicit(t *testing.T) {
	// RUNOPS_ENV=production but no RUNOPS_PROJECT_REGISTRY → fail-fast.
	getenv := envFromMap(map[string]string{
		"RUNOPS_ENV": "production",
	})
	_, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })
	if err == nil {
		t.Fatalf("want error in production without explicit registry, got nil")
	}
}

func TestNewProjectRegistry_FirestoreRequiresProjectID(t *testing.T) {
	// Firestore is now implemented (#0011) but still fail-fast when env is incomplete.
	getenv := envFromMap(map[string]string{
		"RUNOPS_PROJECT_REGISTRY": "firestore",
		// GOOGLE_CLOUD_PROJECT intentionally absent
	})
	_, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })
	if err == nil {
		t.Fatalf("want error when GOOGLE_CLOUD_PROJECT is missing")
	}
	if !strings.Contains(err.Error(), "GOOGLE_CLOUD_PROJECT") {
		t.Errorf("error should mention GOOGLE_CLOUD_PROJECT, got %q", err.Error())
	}
}

func TestNewProjectRegistry_RejectsInvalidValue(t *testing.T) {
	getenv := envFromMap(map[string]string{
		"RUNOPS_PROJECT_REGISTRY": "postgres",
	})
	_, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })
	if err == nil {
		t.Fatalf("want error for invalid value, got nil")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Errorf("error should mention the bad value, got %q", err.Error())
	}
}

func TestNewProjectRegistry_SqliteUsesEnvPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "explicit.db")
	getenv := envFromMap(map[string]string{
		"RUNOPS_PROJECT_REGISTRY": "sqlite",
		"RUNOPS_STATE_DB_PATH":    dbPath,
	})
	reg, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), getenv)
	t.Cleanup(func() { _ = cleanup() })
	if err != nil {
		t.Fatalf("sqlite explicit: %v", err)
	}
	if reg == nil {
		t.Fatalf("want non-nil registry")
	}
}

func TestNewProjectRegistry_ReturnsCleanupOnError(t *testing.T) {
	// Even error paths must return a non-nil cleanup so deferring is safe.
	_, cleanup, err := state.NewProjectRegistryFromEnv(context.Background(), envFromMap(nil))
	if err == nil {
		t.Fatalf("expected error from empty env")
	}
	if cleanup == nil {
		t.Fatalf("cleanup must be non-nil on error path")
	}
	if got := cleanup(); got != nil {
		t.Errorf("noop cleanup should return nil, got %v", got)
	}
}
