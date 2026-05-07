//go:build integration

package state_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"cloud.google.com/go/firestore"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// newFirestoreTest sets up a Firestore client against the emulator and
// returns a registry scoped to a per-test collection name so concurrent
// tests do not collide. The emulator does not support DROP COLLECTION; the
// junk lives until the container is restarted, which is acceptable for CI.
func newFirestoreTest(t *testing.T) (port.ProjectRegistry, func()) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; start emulator with 'just firestore-up'")
	}
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = "runops-local"
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("firestore.NewClient: %v", err)
	}
	collection := "projects_" + uniqueSuffix(t)
	reg := state.NewFirestoreProjectRegistry(client, collection)
	return reg, func() { _ = client.Close() }
}

func uniqueSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

func sampleFirestoreProject(id string) domain.Project {
	return domain.Project{
		ID:                      id,
		GitHubOrg:               "hironow",
		GitHubRepo:              "demo",
		WorkspacePath:           "/home/coder/projects/" + id,
		SlackDefaultChannel:     "#runops",
		GitHubAppInstallationID: 123456,
	}
}

func TestFirestoreProjectRegistry_Add_RejectsInvalidID(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	err := reg.Add(ctx, sampleFirestoreProject("bad id"))
	if !errors.Is(err, domain.ErrInvalidProjectID) {
		t.Errorf("invalid id: want ErrInvalidProjectID, got %v", err)
	}
}

func TestFirestoreProjectRegistry_Add_RejectsDuplicate(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	if err := reg.Add(ctx, sampleFirestoreProject("foo")); err != nil {
		t.Fatalf("first add: %v", err)
	}
	err := reg.Add(ctx, sampleFirestoreProject("foo"))
	if !errors.Is(err, domain.ErrProjectAlreadyExists) {
		t.Errorf("duplicate add: want ErrProjectAlreadyExists, got %v", err)
	}
}

func TestFirestoreProjectRegistry_Get_NotFound(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	_, err := reg.Get(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("get missing: want ErrProjectNotFound, got %v", err)
	}
}

func TestFirestoreProjectRegistry_AddListGetArchive_Lifecycle(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	if err := reg.Add(ctx, sampleFirestoreProject("foo")); err != nil {
		t.Fatalf("add foo: %v", err)
	}
	if err := reg.Add(ctx, sampleFirestoreProject("bar")); err != nil {
		t.Fatalf("add bar: %v", err)
	}

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

	all, err := reg.List(ctx, port.ProjectListFilter{})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("list all: want 2, got %d", len(all))
	}

	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Fatalf("archive foo: %v", err)
	}

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

	active, err := reg.List(ctx, port.ProjectListFilter{Status: domain.ProjectStatusActive})
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].ID != "bar" {
		t.Errorf("list active: %+v", active)
	}

	archived, err := reg.List(ctx, port.ProjectListFilter{Status: domain.ProjectStatusArchived})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived) != 1 || archived[0].ID != "foo" {
		t.Errorf("list archived: %+v", archived)
	}
}

func TestFirestoreProjectRegistry_Archive_NotFound(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	err := reg.Archive(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrProjectNotFound) {
		t.Errorf("archive missing: want ErrProjectNotFound, got %v", err)
	}
}

func TestFirestoreProjectRegistry_Archive_IsIdempotent(t *testing.T) {
	reg, cleanup := newFirestoreTest(t)
	defer cleanup()
	ctx := context.Background()

	if err := reg.Add(ctx, sampleFirestoreProject("foo")); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Fatalf("first archive: %v", err)
	}
	if err := reg.Archive(ctx, "foo"); err != nil {
		t.Errorf("second archive (idempotent): %v", err)
	}
}
