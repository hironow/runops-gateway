package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// fakeAgentSessionRegistry — only Get is exercised; Register / Revoke
// are out of scope for the verifier path.
type fakeAgentSessionRegistry struct {
	sessions map[string]domain.AgentSession
}

func (f *fakeAgentSessionRegistry) Register(_ context.Context, sess domain.AgentSession) error {
	if f.sessions == nil {
		f.sessions = map[string]domain.AgentSession{}
	}
	f.sessions[sess.SessionID] = sess
	return nil
}

func (f *fakeAgentSessionRegistry) Get(_ context.Context, sessionID string) (domain.AgentSession, error) {
	s, ok := f.sessions[sessionID]
	if !ok {
		return domain.AgentSession{}, domain.ErrAgentSessionNotFound
	}
	return s, nil
}

func (f *fakeAgentSessionRegistry) Revoke(_ context.Context, sessionID string) error {
	s, ok := f.sessions[sessionID]
	if !ok {
		return domain.ErrAgentSessionNotFound
	}
	t := time.Now()
	s.RevokedAt = &t
	f.sessions[sessionID] = s
	return nil
}

func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".synthetic-signature-for-test"
}

const brokerAudience = "https://broker.example.com"

func freshClaims(now time.Time, sa string) map[string]any {
	return map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   brokerAudience,
		"sub":   "workspace-daemon-uid",
		"email": sa,
		"exp":   float64(now.Add(time.Hour).Unix()),
	}
}

func freshSession(sa, projectID string, tool domain.Tool, now time.Time) domain.AgentSession {
	return domain.AgentSession{
		SessionID:         "abcdef0123456789abcdef0123456789",
		WorkspaceDaemonSA: sa,
		ProjectID:         projectID,
		Tool:              tool,
		IssuedAt:          now.Add(-time.Hour),
		ExpiresAt:         now.Add(time.Hour),
	}
}

// Happy path: bearer JWT verifies + session found + session verifies →
// BrokerActor{CallerAIAgent, email, sessionID}.
func TestDelegatedAgentVerifier_VerifyAndResolve_HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	sa := "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com"
	registry := &fakeAgentSessionRegistry{}
	sess := freshSession(sa, "proj-foo", domain.ToolPaintress, now)
	_ = registry.Register(context.Background(), sess)

	v := auth.NewDelegatedAgentVerifier(brokerAudience, registry, func() time.Time { return now })
	jwt := makeJWT(freshClaims(now, sa))

	actor, err := v.VerifyAndResolve(context.Background(), jwt, sess.SessionID, "proj-foo", domain.ToolPaintress)
	if err != nil {
		t.Fatalf("happy path: %v", err)
	}
	if actor.Type != domain.CallerAIAgent {
		t.Errorf("Type = %q, want ai-agent", actor.Type)
	}
	if actor.UserEmail != sa {
		t.Errorf("UserEmail = %q", actor.UserEmail)
	}
	if actor.SessionID != sess.SessionID {
		t.Errorf("SessionID = %q", actor.SessionID)
	}
}

// JWT-side failures (parse / audience / expiry) propagate as their
// IdentityToken sentinels — registry is NOT consulted.
func TestDelegatedAgentVerifier_VerifyAndResolve_JWTFailures(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	sa := "sa@x.example"
	cases := map[string]struct {
		jwtFn        func() string
		wantSentinel error
	}{
		"malformed": {
			jwtFn:        func() string { return "not.a.jwt.too.many.dots" },
			wantSentinel: domain.ErrIdentityTokenMalformed,
		},
		"audience mismatch": {
			jwtFn: func() string {
				c := freshClaims(now, sa)
				c["aud"] = "https://attacker.example.com"
				return makeJWT(c)
			},
			wantSentinel: domain.ErrIdentityTokenAudienceMismatch,
		},
		"expired": {
			jwtFn: func() string {
				c := freshClaims(now, sa)
				c["exp"] = float64(now.Add(-time.Minute).Unix())
				return makeJWT(c)
			},
			wantSentinel: domain.ErrIdentityTokenExpired,
		},
	}
	for name, c := range cases {
		registry := &fakeAgentSessionRegistry{}
		v := auth.NewDelegatedAgentVerifier(brokerAudience, registry, func() time.Time { return now })
		_, err := v.VerifyAndResolve(context.Background(), c.jwtFn(), "any-session", "proj-foo", domain.ToolPaintress)
		if !errors.Is(err, c.wantSentinel) {
			t.Errorf("[%s] want %v, got %v", name, c.wantSentinel, err)
		}
		if len(registry.sessions) != 0 {
			t.Errorf("[%s] registry must NOT be consulted on JWT failure", name)
		}
	}
}

// Session-side failures (not found / revoked / expired / SA mismatch /
// project mismatch / tool mismatch) propagate as their AgentSession
// sentinels.
func TestDelegatedAgentVerifier_VerifyAndResolve_SessionFailures(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	sa := "workspace-daemon@gen-ai-hironow.iam.gserviceaccount.com"
	jwt := makeJWT(freshClaims(now, sa))

	type sessMutator func(s *domain.AgentSession)
	cases := map[string]struct {
		mutate       sessMutator
		callProject  string
		callTool     domain.Tool
		callSession  string
		wantSentinel error
	}{
		"not in registry": {
			mutate:       nil,
			callProject:  "proj-foo",
			callTool:     domain.ToolPaintress,
			callSession:  "missing-session-id",
			wantSentinel: domain.ErrAgentSessionNotFound,
		},
		"revoked": {
			mutate:       func(s *domain.AgentSession) { rev := now.Add(-time.Minute); s.RevokedAt = &rev },
			callProject:  "proj-foo",
			callTool:     domain.ToolPaintress,
			callSession:  "abcdef0123456789abcdef0123456789",
			wantSentinel: domain.ErrAgentSessionRevoked,
		},
		"expired": {
			mutate:       func(s *domain.AgentSession) { s.ExpiresAt = now.Add(-time.Minute) },
			callProject:  "proj-foo",
			callTool:     domain.ToolPaintress,
			callSession:  "abcdef0123456789abcdef0123456789",
			wantSentinel: domain.ErrAgentSessionExpired,
		},
		"SA mismatch": {
			mutate:       func(s *domain.AgentSession) { s.WorkspaceDaemonSA = "different-sa@x.example" },
			callProject:  "proj-foo",
			callTool:     domain.ToolPaintress,
			callSession:  "abcdef0123456789abcdef0123456789",
			wantSentinel: domain.ErrAgentSessionSAMismatch,
		},
		"project mismatch": {
			mutate:       nil,
			callProject:  "proj-other",
			callTool:     domain.ToolPaintress,
			callSession:  "abcdef0123456789abcdef0123456789",
			wantSentinel: domain.ErrAgentSessionProjectMismatch,
		},
		"tool mismatch": {
			mutate:       nil,
			callProject:  "proj-foo",
			callTool:     domain.ToolSightjack,
			callSession:  "abcdef0123456789abcdef0123456789",
			wantSentinel: domain.ErrAgentSessionToolMismatch,
		},
	}
	for name, c := range cases {
		registry := &fakeAgentSessionRegistry{}
		sess := freshSession(sa, "proj-foo", domain.ToolPaintress, now)
		if c.mutate != nil {
			c.mutate(&sess)
		}
		_ = registry.Register(context.Background(), sess)

		v := auth.NewDelegatedAgentVerifier(brokerAudience, registry, func() time.Time { return now })
		_, err := v.VerifyAndResolve(context.Background(), jwt, c.callSession, c.callProject, c.callTool)
		if !errors.Is(err, c.wantSentinel) {
			t.Errorf("[%s] want %v, got %v", name, c.wantSentinel, err)
		}
	}
}
