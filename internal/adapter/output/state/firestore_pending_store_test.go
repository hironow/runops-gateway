//go:build integration

package state_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// newFirestorePendingStoreTest mirrors newFirestoreTest but for the
// PendingStore port. Per-test collection names avoid cross-test pollution.
func newFirestorePendingStoreTest(t *testing.T) (port.PendingStore, func()) {
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
	collection := "pending_approvals_" + uniqueSuffix(t)
	store := state.NewFirestorePendingStore(client, collection)
	return store, func() { _ = client.Close() }
}

func samplePending(idempotencyKey string) domain.PendingApproval {
	return domain.PendingApproval{
		IdempotencyKey:       idempotencyKey,
		Op:                   domain.PendingOpAdd,
		BodyJSON:             []byte(`{"id":"foo","github_org":"acme"}`),
		EffectiveRequesterID: "U01234ABCD",
		RequesterActorType:   "human-operator",
	}
}

func TestFirestorePendingStore_CreateThenGet(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	ctx := context.Background()
	key := "test_create_" + uniqueSuffix(t)

	created, err := store.CreateIfNotExists(ctx, samplePending(key))
	if err != nil {
		t.Fatalf("CreateIfNotExists: %v", err)
	}
	if created.Status != domain.PendingStatusPendingApproval {
		t.Errorf("Status = %q, want %q", created.Status, domain.PendingStatusPendingApproval)
	}
	if created.CreatedAt.IsZero() {
		t.Error("CreatedAt should be auto-populated, got zero time")
	}

	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.IdempotencyKey != key {
		t.Errorf("Get returned wrong key: got %q want %q", got.IdempotencyKey, key)
	}
	if got.Op != domain.PendingOpAdd {
		t.Errorf("Op = %q, want %q", got.Op, domain.PendingOpAdd)
	}
}

func TestFirestorePendingStore_CreateIfNotExists_Idempotent(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	ctx := context.Background()
	key := "test_idem_" + uniqueSuffix(t)

	if _, err := store.CreateIfNotExists(ctx, samplePending(key)); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	existing, err := store.CreateIfNotExists(ctx, samplePending(key))
	if !errors.Is(err, port.ErrPendingAlreadyExists) {
		t.Fatalf("second Create err = %v, want ErrPendingAlreadyExists", err)
	}
	if existing.IdempotencyKey != key {
		t.Errorf("returned existing record key = %q, want %q", existing.IdempotencyKey, key)
	}
}

func TestFirestorePendingStore_Get_NotFound(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	_, err := store.Get(context.Background(), "missing_"+uniqueSuffix(t))
	if !errors.Is(err, port.ErrPendingNotFound) {
		t.Errorf("Get err = %v, want ErrPendingNotFound", err)
	}
}

func TestFirestorePendingStore_Transition_PendingToApprovedApplied(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	ctx := context.Background()
	key := "test_apply_" + uniqueSuffix(t)
	if _, err := store.CreateIfNotExists(ctx, samplePending(key)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	now := time.Now().UTC()
	if err := store.Transition(ctx, key, domain.PendingStatusApprovedApplied, &now); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	got, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after transition: %v", err)
	}
	if got.Status != domain.PendingStatusApprovedApplied {
		t.Errorf("Status = %q, want %q", got.Status, domain.PendingStatusApprovedApplied)
	}
	if got.AppliedAt == nil {
		t.Error("AppliedAt should be set after approved_applied transition")
	}
}

func TestFirestorePendingStore_Transition_TerminalRejected(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	ctx := context.Background()
	key := "test_term_" + uniqueSuffix(t)
	if _, err := store.CreateIfNotExists(ctx, samplePending(key)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Transition(ctx, key, domain.PendingStatusDenied, nil); err != nil {
		t.Fatalf("first Transition: %v", err)
	}
	now := time.Now().UTC()
	err := store.Transition(ctx, key, domain.PendingStatusApprovedApplied, &now)
	if !errors.Is(err, port.ErrPendingTerminalTransition) {
		t.Errorf("second Transition err = %v, want ErrPendingTerminalTransition", err)
	}
}

func TestFirestorePendingStore_Transition_NotFound(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	err := store.Transition(
		context.Background(),
		"missing_"+uniqueSuffix(t),
		domain.PendingStatusDenied,
		nil,
	)
	if !errors.Is(err, port.ErrPendingNotFound) {
		t.Errorf("Transition err = %v, want ErrPendingNotFound", err)
	}
}

func TestFirestorePendingStore_Transition_AppliedAtValidation(t *testing.T) {
	store, cleanup := newFirestorePendingStoreTest(t)
	defer cleanup()
	ctx := context.Background()
	key := "test_validation_" + uniqueSuffix(t)
	if _, err := store.CreateIfNotExists(ctx, samplePending(key)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// approved_applied without appliedAt must fail
	if err := store.Transition(ctx, key, domain.PendingStatusApprovedApplied, nil); err == nil {
		t.Error("approved_applied with nil appliedAt should error")
	}
	// denied with appliedAt must fail
	now := time.Now().UTC()
	if err := store.Transition(ctx, key, domain.PendingStatusDenied, &now); err == nil {
		t.Error("denied with non-nil appliedAt should error")
	}
}
