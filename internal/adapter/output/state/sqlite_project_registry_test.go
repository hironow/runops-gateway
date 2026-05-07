package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

func newTestRegistry(t *testing.T) (port.ProjectRegistry, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := state.OpenSQLite(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	reg := state.NewSQLiteProjectRegistry(db)
	return reg, func() { _ = db.Close() }
}

func sampleProject(id string) domain.Project {
	return domain.Project{
		ID:                      id,
		GitHubOrg:               "hironow",
		GitHubRepo:              "demo",
		WorkspacePath:           "/home/coder/projects/" + id,
		SlackDefaultChannel:     "#runops",
		GitHubAppInstallationID: 123456,
	}
}

func TestSQLiteProjectRegistry_Add_RejectsInvalidID(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	bad := sampleProject("invalid id with spaces")
	err := reg.Add(ctx, bad)
	if !errors.Is(err, domain.ErrInvalidProjectID) {
		t.Errorf("Add invalid id: want ErrInvalidProjectID, got %v", err)
	}
}

func TestSQLiteProjectRegistry_Add_RejectsDuplicate(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	if err := reg.Add(ctx, sampleProject("foo")); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := reg.Add(ctx, sampleProject("foo"))
	if !errors.Is(err, domain.ErrProjectAlreadyExists) {
		t.Errorf("Add duplicate: want ErrProjectAlreadyExists, got %v", err)
	}
}

func TestSQLiteProjectRegistry_Get_NotFound(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	_, err := reg.Get(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("Get missing: want ErrProjectNotFound, got %v", err)
	}
}

func TestSQLiteProjectRegistry_AddListGetArchive_Lifecycle(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	// Given: foo + bar both added.
	if err := reg.Add(ctx, sampleProject("foo")); err != nil {
		t.Fatalf("add foo: %v", err)
	}
	if err := reg.Add(ctx, sampleProject("bar")); err != nil {
		t.Fatalf("add bar: %v", err)
	}

	// Get: round-trips field values + status defaults to active + created_at set.
	got, err := reg.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("get foo: %v", err)
	}
	if got.ID != "foo" || got.GitHubOrg != "hironow" || got.GitHubRepo != "demo" {
		t.Errorf("get foo round-trip: %+v", got)
	}
	if got.Status != domain.ProjectStatusActive {
		t.Errorf("default status: want active, got %s", got.Status)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be set")
	}
	if got.ArchivedAt != nil {
		t.Errorf("ArchivedAt should be nil for active, got %v", got.ArchivedAt)
	}

	// List all (filter empty).
	all, err := reg.List(ctx, port.ProjectListFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list all: want 2, got %d", len(all))
	}

	// Archive foo.
	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Fatalf("archive foo: %v", err)
	}

	// Get foo again: status archived + ArchivedAt set.
	got, err = reg.Get(ctx, "foo")
	if err != nil {
		t.Fatalf("get archived foo: %v", err)
	}
	if got.Status != domain.ProjectStatusArchived {
		t.Errorf("after archive: status = %s, want archived", got.Status)
	}
	if got.ArchivedAt == nil {
		t.Errorf("ArchivedAt should be non-nil after archive")
	}

	// List active: only bar.
	active, err := reg.List(ctx, port.ProjectListFilter{Status: domain.ProjectStatusActive})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].ID != "bar" {
		t.Errorf("list active: %+v", active)
	}

	// List archived: only foo.
	archived, err := reg.List(ctx, port.ProjectListFilter{Status: domain.ProjectStatusArchived})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != "foo" {
		t.Errorf("list archived: %+v", archived)
	}
}

func TestSQLiteProjectRegistry_Archive_NotFound(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	err := reg.Archive(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("Archive missing: want ErrProjectNotFound, got %v", err)
	}
}

func TestSQLiteProjectRegistry_Archive_IsIdempotent(t *testing.T) {
	reg, cleanup := newTestRegistry(t)
	defer cleanup()
	ctx := context.Background()

	if err := reg.Add(ctx, sampleProject("foo")); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	// Second archive on already-archived project is a no-op (no error).
	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Errorf("second archive (idempotent): %v", err)
	}
}
