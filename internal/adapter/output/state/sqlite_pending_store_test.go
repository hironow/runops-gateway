package state_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

func newTestPendingStore(t *testing.T) (port.PendingStore, func()) {
	t.Helper()
	ctx := context.Background()
	db, err := state.OpenSQLite(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	store := state.NewSQLitePendingStore(db)
	return store, func() { _ = db.Close() }
}

func samplePending(key string) domain.PendingApproval {
	return domain.PendingApproval{
		IdempotencyKey:     key,
		Op:                 domain.PendingOpAdd,
		BodyJSON:           []byte(`{"id":"foo","github_org":"acme"}`),
		RequesterActorType: "human-operator",
	}
}

func TestSQLitePendingStore_CreateThenGet(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	ctx := context.Background()

	created, err := store.CreateIfNotExists(ctx, samplePending("k1"))
	if err != nil {
		t.Fatalf("CreateIfNotExists: %v", err)
	}
	if created.Status != domain.PendingStatusPendingApproval {
		t.Errorf("Status = %q, want pending_approval", created.Status)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt should be auto-populated")
	}

	got, err := store.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IdempotencyKey != "k1" || got.Op != domain.PendingOpAdd {
		t.Errorf("Get returned unexpected record: %+v", got)
	}
	if string(got.BodyJSON) != `{"id":"foo","github_org":"acme"}` {
		t.Errorf("BodyJSON roundtrip mismatch: %q", got.BodyJSON)
	}
}

func TestSQLitePendingStore_CreateIfNotExists_Idempotent(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.CreateIfNotExists(ctx, samplePending("k_idem")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	existing, err := store.CreateIfNotExists(ctx, samplePending("k_idem"))
	if !errors.Is(err, port.ErrPendingAlreadyExists) {
		t.Fatalf("second Create err = %v, want ErrPendingAlreadyExists", err)
	}
	if existing.IdempotencyKey != "k_idem" {
		t.Errorf("returned existing record key = %q, want k_idem", existing.IdempotencyKey)
	}
}

func TestSQLitePendingStore_Get_NotFound(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	_, err := store.Get(context.Background(), "missing")
	if !errors.Is(err, port.ErrPendingNotFound) {
		t.Errorf("Get err = %v, want ErrPendingNotFound", err)
	}
}

func TestSQLitePendingStore_Transition_PendingToApprovedApplied(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.CreateIfNotExists(ctx, samplePending("k_apply")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Transition(ctx, "k_apply", domain.PendingStatusApprovedApplied, &now); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	got, err := store.Get(ctx, "k_apply")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.PendingStatusApprovedApplied {
		t.Errorf("Status = %q, want approved_applied", got.Status)
	}
	if got.AppliedAt == nil {
		t.Error("AppliedAt must be set after approved_applied transition")
	}
}

func TestSQLitePendingStore_Transition_TerminalRejected(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.CreateIfNotExists(ctx, samplePending("k_term")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Transition(ctx, "k_term", domain.PendingStatusDenied, nil); err != nil {
		t.Fatalf("first Transition denied: %v", err)
	}
	now := time.Now().UTC()
	err := store.Transition(ctx, "k_term", domain.PendingStatusApprovedApplied, &now)
	if !errors.Is(err, port.ErrPendingTerminalTransition) {
		t.Errorf("second Transition err = %v, want ErrPendingTerminalTransition", err)
	}
}

func TestSQLitePendingStore_Transition_NotFound(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	err := store.Transition(
		context.Background(), "missing",
		domain.PendingStatusDenied, nil,
	)
	if !errors.Is(err, port.ErrPendingNotFound) {
		t.Errorf("Transition err = %v, want ErrPendingNotFound", err)
	}
}

func TestSQLitePendingStore_Transition_AppliedAtValidation(t *testing.T) {
	store, cleanup := newTestPendingStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := store.CreateIfNotExists(ctx, samplePending("k_val")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// approved_applied without appliedAt must fail
	if err := store.Transition(ctx, "k_val", domain.PendingStatusApprovedApplied, nil); err == nil {
		t.Error("approved_applied with nil appliedAt should error")
	}
	// denied with appliedAt must fail
	now := time.Now().UTC()
	if err := store.Transition(ctx, "k_val", domain.PendingStatusDenied, &now); err == nil {
		t.Error("denied with non-nil appliedAt should error")
	}
}
