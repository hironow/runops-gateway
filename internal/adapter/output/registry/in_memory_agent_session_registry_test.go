package registry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/registry"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

func sessionFor(t *testing.T, id, sa, projectID string, tool domain.Tool, now time.Time) domain.AgentSession {
	t.Helper()
	return domain.AgentSession{
		SessionID:         id,
		WorkspaceDaemonSA: sa,
		ProjectID:         projectID,
		Tool:              tool,
		IssuedAt:          now.Add(-time.Hour),
		ExpiresAt:         now.Add(time.Hour),
	}
}

// Register + Get round-trip: a registered session is retrievable
// from the same registry instance.
func TestInMemoryAgentSessionRegistry_RegisterAndGet(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	now := time.Now()
	s := sessionFor(t, "abcdef0123456789abcdef0123456789", "sa@x.example", "proj-foo", domain.ToolPaintress, now)
	if err := r.Register(context.Background(), s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := r.Get(context.Background(), s.SessionID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SessionID != s.SessionID || got.WorkspaceDaemonSA != s.WorkspaceDaemonSA || got.ProjectID != s.ProjectID || got.Tool != s.Tool {
		t.Errorf("got = %+v, want %+v", got, s)
	}
}

// Register MUST reject a duplicate SessionID — the workspace daemon
// owns the lifecycle and idempotent registration would mask
// concurrent-create bugs (plan v8 §5.2 design note in the port).
func TestInMemoryAgentSessionRegistry_RegisterDuplicateRejected(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	now := time.Now()
	s := sessionFor(t, "duplicate-session-id-padding-here", "sa@x.example", "proj", domain.ToolPaintress, now)
	if err := r.Register(context.Background(), s); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(context.Background(), s); err == nil {
		t.Errorf("duplicate Register must error, got nil")
	}
}

// Get for an unknown SessionID returns the domain sentinel so the
// verifier's error-mapping branches surface the right audit signal.
func TestInMemoryAgentSessionRegistry_GetUnknownReturnsSentinel(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	_, err := r.Get(context.Background(), "missing-session-id")
	if !errors.Is(err, domain.ErrAgentSessionNotFound) {
		t.Errorf("want ErrAgentSessionNotFound, got %v", err)
	}
}

// Revoke flips RevokedAt non-nil and a follow-up Get exposes the
// revocation timestamp so domain.VerifyAgentSession can return
// ErrAgentSessionRevoked.
func TestInMemoryAgentSessionRegistry_RevokeMarksSession(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	now := time.Now()
	s := sessionFor(t, "revoke-target-id-padding-12345678", "sa@x.example", "proj", domain.ToolPaintress, now)
	_ = r.Register(context.Background(), s)
	if err := r.Revoke(context.Background(), s.SessionID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, _ := r.Get(context.Background(), s.SessionID)
	if got.RevokedAt == nil {
		t.Errorf("RevokedAt must be non-nil after Revoke")
	}
}

// Revoke is idempotent: revoking an already-revoked session must
// NOT error (plan v8 §5.2 contract on the port).
func TestInMemoryAgentSessionRegistry_RevokeIdempotent(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	now := time.Now()
	s := sessionFor(t, "revoke-twice-id-padding-12345678", "sa@x.example", "proj", domain.ToolPaintress, now)
	_ = r.Register(context.Background(), s)
	if err := r.Revoke(context.Background(), s.SessionID); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := r.Revoke(context.Background(), s.SessionID); err != nil {
		t.Errorf("second Revoke must be idempotent, got %v", err)
	}
}

// Revoke for an unknown SessionID surfaces the sentinel so callers
// (typically admin tooling) can distinguish "session not in registry"
// from "session already revoked".
func TestInMemoryAgentSessionRegistry_RevokeUnknownReturnsSentinel(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	err := r.Revoke(context.Background(), "missing-session-id")
	if !errors.Is(err, domain.ErrAgentSessionNotFound) {
		t.Errorf("want ErrAgentSessionNotFound, got %v", err)
	}
}

// Concurrent Register / Get / Revoke must be race-free. The race
// detector covers this when running with `go test -race`.
func TestInMemoryAgentSessionRegistry_ConcurrentAccessRaceFree(t *testing.T) {
	r := registry.NewInMemoryAgentSessionRegistry()
	now := time.Now()
	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			id := "id-padding-prefix-12345" + string(rune('a'+i%26)) + string(rune('0'+i%10))
			s := sessionFor(t, id, "sa@x.example", "proj", domain.ToolPaintress, now)
			_ = r.Register(context.Background(), s)
			_, _ = r.Get(context.Background(), id)
			if i%3 == 0 {
				_ = r.Revoke(context.Background(), id)
			}
		}(i)
	}
	wg.Wait()
}
