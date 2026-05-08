package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// makeJWT builds a synthetic JWT for tests inside `package auth`
// (the white-box ones that need access to unexported ctors).
// Re-declared here because the equivalent helper in
// jwks_verifier_test.go lives in `package auth_test` and is not
// reachable across the package boundary.
func makeJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".synthetic-signature-for-test"
}

func makeRequest(t *testing.T, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/broker/token", strings.NewReader(""))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

// fakeAgentRegistry is a minimal port.AgentSessionRegistry impl for
// the ai-agent dispatch test. Reused from the delegated_agent
// test pattern but redefined here to avoid cross-test coupling.
type fakeAgentRegistry struct {
	sess domain.AgentSession
	ok   bool
}

func (f *fakeAgentRegistry) Register(_ context.Context, _ domain.AgentSession) error {
	return nil
}

func (f *fakeAgentRegistry) Get(_ context.Context, _ string) (domain.AgentSession, error) {
	if !f.ok {
		return domain.AgentSession{}, domain.ErrAgentSessionNotFound
	}
	return f.sess, nil
}

func (f *fakeAgentRegistry) Revoke(_ context.Context, _ string) error {
	return nil
}

func newTestChain(t *testing.T) (*ChainAuthenticator, *fakeAgentRegistry) {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) }

	gcloud := newWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://accounts.google.com",
			Email:  "operator@example.com",
		}},
		"https://accounts.google.com",
		[]string{"operator@example.com"},
	)
	cloudrun := newCloudRunIAMVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://accounts.google.com",
			Email:  "gateway-internal@example.iam.gserviceaccount.com",
		}},
		"https://accounts.google.com",
		[]string{"gateway-internal@example.iam.gserviceaccount.com"},
	)
	workload := newWorkloadIdentityVerifierWithVerifier(
		&fakeSignatureVerifier{claims: domain.IdentityClaims{
			Issuer: "https://accounts.google.com",
			Email:  "workspace-daemon@example.iam.gserviceaccount.com",
		}},
		"https://accounts.google.com",
		[]string{"workspace-daemon@example.iam.gserviceaccount.com"},
	)
	registry := &fakeAgentRegistry{}
	delegated := NewDelegatedAgentVerifier("https://broker.example.com", registry, now)

	chain := NewChainAuthenticator(gcloud, cloudrun, workload, delegated)
	return chain, registry
}

func bearerHeaders(callerType, jwt string) map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + jwt,
		"X-Broker-Caller-Type": callerType,
	}
}

// Each caller type dispatches to the right verifier and returns
// the expected BrokerActor.Type discriminator. The ai-agent path
// uses a real (synthetic but well-formed) JWT because
// DelegatedAgentVerifier calls domain.ParseIdentityClaims directly
// rather than through the bearer-only verifiers' fakeSignatureVerifier
// seam.
func TestChainAuthenticator_Authenticate_DispatchesEachCallerType(t *testing.T) {
	chain, registry := newTestChain(t)
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	registry.ok = true
	registry.sess = domain.AgentSession{
		SessionID:         "abcdef0123456789abcdef0123456789",
		WorkspaceDaemonSA: "workspace-daemon@example.iam.gserviceaccount.com",
		ProjectID:         "proj-foo",
		Tool:              domain.ToolPaintress,
		IssuedAt:          now.Add(-time.Hour),
		ExpiresAt:         now.Add(time.Hour),
	}
	aiAgentJWT := makeJWT(map[string]any{
		"iss":   "https://accounts.google.com",
		"aud":   "https://broker.example.com",
		"sub":   "workspace-daemon-uid",
		"email": "workspace-daemon@example.iam.gserviceaccount.com",
		"exp":   float64(now.Add(time.Hour).Unix()),
	})

	cases := []struct {
		name       string
		callerType string
		bearer     string
		extra      map[string]string
		project    string
		tool       domain.Tool
		wantType   domain.CallerType
	}{
		{name: "human-operator", callerType: "human-operator", bearer: "any-jwt", project: "proj-foo", tool: domain.ToolPaintress, wantType: domain.CallerHumanOperator},
		{name: "gateway-service", callerType: "gateway-service", bearer: "any-jwt", project: "proj-foo", tool: domain.ToolPaintress, wantType: domain.CallerGatewayService},
		{name: "workspace-daemon", callerType: "workspace-daemon", bearer: "any-jwt", project: "proj-foo", tool: domain.ToolPaintress, wantType: domain.CallerWorkspaceDaemon},
		{name: "ai-agent", callerType: "ai-agent", bearer: aiAgentJWT, extra: map[string]string{"X-Broker-Session-Id": "abcdef0123456789abcdef0123456789"}, project: "proj-foo", tool: domain.ToolPaintress, wantType: domain.CallerAIAgent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			headers := bearerHeaders(c.callerType, c.bearer)
			for k, v := range c.extra {
				headers[k] = v
			}
			actor, err := chain.Authenticate(makeRequest(t, headers), c.project, c.tool)
			if err != nil {
				t.Fatalf("[%s]: %v", c.name, err)
			}
			if actor.Type != c.wantType {
				t.Errorf("[%s] Type = %q, want %q", c.name, actor.Type, c.wantType)
			}
		})
	}
}

// Missing X-Broker-Caller-Type header → 401 (= ErrCallerTypeMissing).
// The chain refuses to guess the caller type from the bearer alone
// because that would defeat the per-caller policy.
func TestChainAuthenticator_Authenticate_MissingCallerTypeRejected(t *testing.T) {
	chain, _ := newTestChain(t)
	headers := map[string]string{"Authorization": "Bearer any-jwt"}
	_, err := chain.Authenticate(makeRequest(t, headers), "proj", domain.ToolPaintress)
	if !errors.Is(err, ErrCallerTypeMissing) {
		t.Errorf("want ErrCallerTypeMissing, got %v", err)
	}
}

// Unknown X-Broker-Caller-Type value → ErrCallerTypeUnknown so the
// audit log distinguishes "header missing" (= caller bug) from
// "header lies" (= attack-shaped).
func TestChainAuthenticator_Authenticate_UnknownCallerTypeRejected(t *testing.T) {
	chain, _ := newTestChain(t)
	headers := bearerHeaders("not-a-real-caller", "any-jwt")
	_, err := chain.Authenticate(makeRequest(t, headers), "proj", domain.ToolPaintress)
	if !errors.Is(err, ErrCallerTypeUnknown) {
		t.Errorf("want ErrCallerTypeUnknown, got %v", err)
	}
}

// Missing Authorization header → ErrBearerMissing.
func TestChainAuthenticator_Authenticate_MissingAuthorizationRejected(t *testing.T) {
	chain, _ := newTestChain(t)
	headers := map[string]string{"X-Broker-Caller-Type": "human-operator"}
	_, err := chain.Authenticate(makeRequest(t, headers), "proj", domain.ToolPaintress)
	if !errors.Is(err, ErrBearerMissing) {
		t.Errorf("want ErrBearerMissing, got %v", err)
	}
}

// Malformed Authorization header (no "Bearer " prefix) → ErrBearerMalformed.
// The chain will not accept "Basic ..." or raw token values; only
// the RFC 6750 Bearer scheme is supported.
func TestChainAuthenticator_Authenticate_MalformedAuthorizationRejected(t *testing.T) {
	chain, _ := newTestChain(t)
	for _, header := range []string{"raw-jwt-no-prefix", "Basic dXNlcjpwYXNz", "Bearer", "Bearer "} {
		headers := map[string]string{
			"Authorization":        header,
			"X-Broker-Caller-Type": "human-operator",
		}
		_, err := chain.Authenticate(makeRequest(t, headers), "proj", domain.ToolPaintress)
		if !errors.Is(err, ErrBearerMalformed) {
			t.Errorf("Authorization=%q: want ErrBearerMalformed, got %v", header, err)
		}
	}
}

// ai-agent caller without X-Broker-Session-Id header →
// ErrAIAgentMissingSessionID.
func TestChainAuthenticator_Authenticate_AIAgentMissingSessionIDRejected(t *testing.T) {
	chain, _ := newTestChain(t)
	headers := bearerHeaders("ai-agent", "any-jwt")
	_, err := chain.Authenticate(makeRequest(t, headers), "proj", domain.ToolPaintress)
	if !errors.Is(err, ErrAIAgentMissingSessionID) {
		t.Errorf("want ErrAIAgentMissingSessionID, got %v", err)
	}
}
