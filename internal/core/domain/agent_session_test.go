package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// NewAgentSession should produce a session with a non-empty
// SessionID, the requested SA / project / tool, and IssuedAt /
// ExpiresAt that bracket the requested TTL. RevokedAt must start
// nil — Revoke is a separate explicit action.
func TestNewAgentSession_ProducesValidSessionWithExpiry(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	ttl := 24 * time.Hour
	sess, err := domain.NewAgentSession("workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com", "proj-foo", domain.ToolPaintress, ttl, now)
	if err != nil {
		t.Fatalf("NewAgentSession error: %v", err)
	}
	if sess.SessionID == "" {
		t.Errorf("SessionID must be non-empty")
	}
	if !sess.IssuedAt.Equal(now) {
		t.Errorf("IssuedAt = %v, want %v", sess.IssuedAt, now)
	}
	if !sess.ExpiresAt.Equal(now.Add(ttl)) {
		t.Errorf("ExpiresAt = %v, want %v", sess.ExpiresAt, now.Add(ttl))
	}
	if sess.RevokedAt != nil {
		t.Errorf("RevokedAt must start nil, got %v", *sess.RevokedAt)
	}
	if sess.WorkspaceDaemonSA != "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com" || sess.ProjectID != "proj-foo" || sess.Tool != domain.ToolPaintress {
		t.Errorf("Session fields not set: %+v", sess)
	}
}

// SessionID format: 32-character lowercase hex (= 16 random bytes).
// This shape is required by the registry's Firestore document path
// and by the audit-log correlation key.
func TestNewAgentSession_SessionIDIs32CharLowerHex(t *testing.T) {
	sess, err := domain.NewAgentSession("sa@example.com", "proj-x", domain.ToolSightjack, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("NewAgentSession: %v", err)
	}
	if len(sess.SessionID) != 32 {
		t.Errorf("SessionID length = %d, want 32", len(sess.SessionID))
	}
	if strings.Trim(sess.SessionID, "0123456789abcdef") != "" {
		t.Errorf("SessionID must be lowercase hex, got %q", sess.SessionID)
	}
}

// NewAgentSession rejects empty / invalid inputs at the domain
// boundary so the registry never persists garbage.
func TestNewAgentSession_RejectsInvalidInputs(t *testing.T) {
	cases := map[string]struct {
		sa, projectID string
		tool          domain.Tool
		ttl           time.Duration
	}{
		"empty SA":       {"", "proj", domain.ToolPaintress, time.Hour},
		"empty project":  {"sa@x.example", "", domain.ToolPaintress, time.Hour},
		"phonewave tool": {"sa@x.example", "proj", domain.ToolPhonewave, time.Hour},
		"zero TTL":       {"sa@x.example", "proj", domain.ToolPaintress, 0},
		"negative TTL":   {"sa@x.example", "proj", domain.ToolPaintress, -time.Hour},
	}
	for name, c := range cases {
		_, err := domain.NewAgentSession(c.sa, c.projectID, c.tool, c.ttl, time.Now())
		if err == nil {
			t.Errorf("[%s] want error, got nil", name)
		}
	}
}

func validSession(now time.Time) domain.AgentSession {
	return domain.AgentSession{
		SessionID:         "abcdef0123456789abcdef0123456789",
		WorkspaceDaemonSA: "sa@example.com",
		ProjectID:         "proj-foo",
		Tool:              domain.ToolPaintress,
		IssuedAt:          now.Add(-time.Hour),
		ExpiresAt:         now.Add(time.Hour),
	}
}

// VerifyAgentSession happy path: every field matches and the
// session is within its validity window.
func TestVerifyAgentSession_HappyPathReturnsNil(t *testing.T) {
	now := time.Now()
	if err := domain.VerifyAgentSession(validSession(now), "sa@example.com", "proj-foo", domain.ToolPaintress, now); err != nil {
		t.Errorf("happy-path Verify: %v", err)
	}
}

// Each verify failure must produce its own sentinel so the broker /
// audit log can distinguish attack categories.
func TestVerifyAgentSession_RejectsEachMismatch(t *testing.T) {
	now := time.Now()
	cases := map[string]struct {
		mutate       func(s *domain.AgentSession)
		claimedSA    string
		projectID    string
		tool         domain.Tool
		wantSentinel error
	}{
		"revoked": {
			mutate:       func(s *domain.AgentSession) { rev := now.Add(-time.Minute); s.RevokedAt = &rev },
			claimedSA:    "sa@example.com",
			projectID:    "proj-foo",
			tool:         domain.ToolPaintress,
			wantSentinel: domain.ErrAgentSessionRevoked,
		},
		"expired": {
			mutate:       func(s *domain.AgentSession) { s.ExpiresAt = now.Add(-time.Minute) },
			claimedSA:    "sa@example.com",
			projectID:    "proj-foo",
			tool:         domain.ToolPaintress,
			wantSentinel: domain.ErrAgentSessionExpired,
		},
		"sa mismatch": {
			mutate:       func(_ *domain.AgentSession) {},
			claimedSA:    "attacker@evil.example",
			projectID:    "proj-foo",
			tool:         domain.ToolPaintress,
			wantSentinel: domain.ErrAgentSessionSAMismatch,
		},
		"project mismatch": {
			mutate:       func(_ *domain.AgentSession) {},
			claimedSA:    "sa@example.com",
			projectID:    "proj-other",
			tool:         domain.ToolPaintress,
			wantSentinel: domain.ErrAgentSessionProjectMismatch,
		},
		"tool mismatch": {
			mutate:       func(_ *domain.AgentSession) {},
			claimedSA:    "sa@example.com",
			projectID:    "proj-foo",
			tool:         domain.ToolSightjack,
			wantSentinel: domain.ErrAgentSessionToolMismatch,
		},
	}
	for name, c := range cases {
		sess := validSession(now)
		c.mutate(&sess)
		err := domain.VerifyAgentSession(sess, c.claimedSA, c.projectID, c.tool, now)
		if !errors.Is(err, c.wantSentinel) {
			t.Errorf("[%s] want %v, got %v", name, c.wantSentinel, err)
		}
	}
}
