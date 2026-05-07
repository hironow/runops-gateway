package auth

import (
	"context"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// DelegatedAgentVerifier authenticates AI-agent callers (refs#0007
// plan v8 §5.2). It composes:
//
//  1. domain.ParseIdentityClaims + Validate against the broker
//     audience for the workspace-daemon service-account identity
//     token (= the bearer the AI agent presents on behalf of its
//     workspace daemon).
//  2. AgentSessionRegistry.Get for the request's session_id.
//  3. domain.VerifyAgentSession to confirm the session still
//     binds the same (SA, project, tool) the caller is asking
//     about — and is neither revoked nor expired.
//
// On success the verifier returns a domain.BrokerActor of
// CallerAIAgent type with the SA email and SessionID populated;
// the use case + handler trust this actor unconditionally per
// plan v8 §5.4 schema lockdown.
//
// Phase 2d-2a (this file) does NOT verify the JWT signature —
// that lands in Phase 2d-2b once the Google STS / workspace
// JWKs fetcher is wired in. Until then the verifier MUST run
// behind a transport-level auth (Cloud Run IAM ingress) so the
// upstream guarantees the bearer's authenticity.
type DelegatedAgentVerifier struct {
	audience string
	registry port.AgentSessionRegistry
	now      func() time.Time
}

// NewDelegatedAgentVerifier wires the dependencies. now() is
// injectable so tests can pin time without sleeping.
func NewDelegatedAgentVerifier(audience string, registry port.AgentSessionRegistry, now func() time.Time) *DelegatedAgentVerifier {
	if now == nil {
		now = time.Now
	}
	return &DelegatedAgentVerifier{audience: audience, registry: registry, now: now}
}

// VerifyAndResolve runs the full AI-agent verification pipeline
// and returns the resolved BrokerActor on success. Failure modes
// surface as the IdentityToken / AgentSession sentinels from the
// domain package so the broker handler can render a precise audit
// signal (attack-shaped vs lifecycle).
func (v *DelegatedAgentVerifier) VerifyAndResolve(ctx context.Context, bearerJWT, sessionID, projectID string, tool domain.Tool) (domain.BrokerActor, error) {
	now := v.now()
	claims, err := domain.ParseIdentityClaims(bearerJWT)
	if err != nil {
		return domain.BrokerActor{}, err
	}
	if err := claims.Validate(now, v.audience); err != nil {
		return domain.BrokerActor{}, err
	}
	sess, err := v.registry.Get(ctx, sessionID)
	if err != nil {
		return domain.BrokerActor{}, err
	}
	if err := domain.VerifyAgentSession(sess, claims.Email, projectID, tool, now); err != nil {
		return domain.BrokerActor{}, err
	}
	return domain.BrokerActor{
		Type:      domain.CallerAIAgent,
		UserEmail: claims.Email,
		SessionID: sessionID,
	}, nil
}
