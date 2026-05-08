package registry_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/hironow/runops-gateway/internal/adapter/output/registry"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// newFirestoreAgentSessionRegistry boots a Firestore client against
// the emulator and returns a registry scoped to a per-test
// collection so concurrent CI tests do not collide.
func newFirestoreAgentSessionRegistry(t *testing.T) *registry.FirestoreAgentSessionRegistry {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; start emulator with 'just firestore-up'")
	}
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = "runops-local"
	}
	client, err := firestore.NewClient(context.Background(), projectID)
	if err != nil {
		t.Fatalf("firestore.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	rndBytes := make([]byte, 8)
	if _, err := rand.Read(rndBytes); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	collection := "broker_agent_sessions_test_" + hex.EncodeToString(rndBytes)

	r, err := registry.NewFirestoreAgentSessionRegistry(client, collection)
	if err != nil {
		t.Fatalf("NewFirestoreAgentSessionRegistry: %v", err)
	}
	return r
}

func makeFirestoreSession(suffix string) domain.AgentSession {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.AgentSession{
		SessionID:         "abcdef0123456789abcdef012345678" + suffix,
		WorkspaceDaemonSA: "workspace-daemon@example.iam.gserviceaccount.com",
		ProjectID:         "proj-foo",
		Tool:              domain.ToolPaintress,
		IssuedAt:          now.Add(-time.Hour),
		ExpiresAt:         now.Add(time.Hour),
	}
}

// Register + Get round-trip: a registered session is retrievable
// from the same registry instance, with all fields preserved.
func TestFirestoreAgentSessionRegistry_RegisterAndGet(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	ctx := context.Background()
	want := makeFirestoreSession("0")

	if err := r.Register(ctx, want); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get(ctx, want.SessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SessionID != want.SessionID || got.WorkspaceDaemonSA != want.WorkspaceDaemonSA ||
		got.ProjectID != want.ProjectID || got.Tool != want.Tool {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// Duplicate Register MUST fail (Phase 2c-1 contract: workspace daemon
// owns the lifecycle, idempotent Register would mask concurrent-create
// bugs).
func TestFirestoreAgentSessionRegistry_RegisterDuplicateRejected(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	ctx := context.Background()
	s := makeFirestoreSession("1")

	if err := r.Register(ctx, s); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(ctx, s); err == nil {
		t.Errorf("duplicate Register must error, got nil")
	}
}

// Get for an unknown SessionID surfaces ErrAgentSessionNotFound.
func TestFirestoreAgentSessionRegistry_GetUnknownReturnsSentinel(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	_, err := r.Get(context.Background(), "session-id-that-does-not-exist-padding")
	if !errors.Is(err, domain.ErrAgentSessionNotFound) {
		t.Errorf("want ErrAgentSessionNotFound, got %v", err)
	}
}

// Revoke flips revoked_at non-nil and a follow-up Get exposes the
// timestamp so domain.VerifyAgentSession can return
// ErrAgentSessionRevoked.
func TestFirestoreAgentSessionRegistry_RevokeMarksSession(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	ctx := context.Background()
	s := makeFirestoreSession("2")

	if err := r.Register(ctx, s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Revoke(ctx, s.SessionID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, err := r.Get(ctx, s.SessionID)
	if err != nil {
		t.Fatalf("Get post-revoke: %v", err)
	}
	if got.RevokedAt == nil {
		t.Errorf("RevokedAt must be non-nil after Revoke")
	}
}

// Revoke is idempotent — second call must NOT error AND must NOT
// overwrite the original revocation timestamp (Phase 2c-1 contract).
func TestFirestoreAgentSessionRegistry_RevokeIdempotent(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	ctx := context.Background()
	s := makeFirestoreSession("3")

	if err := r.Register(ctx, s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Revoke(ctx, s.SessionID); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	first, _ := r.Get(ctx, s.SessionID)
	firstRevokedAt := *first.RevokedAt

	// Sleep enough to detect a re-write if it happened.
	time.Sleep(100 * time.Millisecond)

	if err := r.Revoke(ctx, s.SessionID); err != nil {
		t.Errorf("second Revoke must be idempotent, got %v", err)
	}
	second, _ := r.Get(ctx, s.SessionID)
	if !second.RevokedAt.Equal(firstRevokedAt) {
		t.Errorf("RevokedAt must NOT change on idempotent re-revoke; got %v vs %v", *second.RevokedAt, firstRevokedAt)
	}
}

// Revoke for an unknown SessionID surfaces the sentinel.
func TestFirestoreAgentSessionRegistry_RevokeUnknownReturnsSentinel(t *testing.T) {
	r := newFirestoreAgentSessionRegistry(t)
	err := r.Revoke(context.Background(), "missing-session-id-padding-xxxxx")
	if !errors.Is(err, domain.ErrAgentSessionNotFound) {
		t.Errorf("want ErrAgentSessionNotFound, got %v", err)
	}
}

// Cross-instance multi-instance contract simulation: two
// independent firestore.Client + registry pairs sharing the same
// collection observe each other's writes. This is THE reason the
// Firestore variant exists (the in-memory variant fails this test
// by design — instances do not share state).
func TestFirestoreAgentSessionRegistry_MultiInstanceVisibility(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set")
	}
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = "runops-local"
	}
	rndBytes := make([]byte, 8)
	_, _ = rand.Read(rndBytes)
	shared := "broker_agent_sessions_shared_" + hex.EncodeToString(rndBytes)

	clientA, err := firestore.NewClient(context.Background(), projectID)
	if err != nil {
		t.Fatalf("clientA: %v", err)
	}
	t.Cleanup(func() { _ = clientA.Close() })
	clientB, err := firestore.NewClient(context.Background(), projectID)
	if err != nil {
		t.Fatalf("clientB: %v", err)
	}
	t.Cleanup(func() { _ = clientB.Close() })

	rA, err := registry.NewFirestoreAgentSessionRegistry(clientA, shared)
	if err != nil {
		t.Fatalf("rA: %v", err)
	}
	rB, err := registry.NewFirestoreAgentSessionRegistry(clientB, shared)
	if err != nil {
		t.Fatalf("rB: %v", err)
	}

	ctx := context.Background()
	s := makeFirestoreSession("4")
	if err := rA.Register(ctx, s); err != nil {
		t.Fatalf("rA.Register: %v", err)
	}
	got, err := rB.Get(ctx, s.SessionID)
	if err != nil {
		t.Fatalf("rB.Get: %v", err)
	}
	if got.SessionID != s.SessionID {
		t.Errorf("multi-instance Get returned wrong session; got %+v", got)
	}
}
