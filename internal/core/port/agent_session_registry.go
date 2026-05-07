// Package port — AI agent session registry secondary port.
//
// Separate compilation unit from port.go because ADR 0033's
// release-gate path glob `internal/core/port/*agent_session*`
// (added in this PR) escalates any change here to auth_boundary.
// Mirrors the rationale for github_token_broker.go: keeping the
// auth-critical interfaces in their own files lets routine port.go
// edits stay outside the meta-gate.
package port

import (
	"context"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// AgentSessionRegistry persists the broker-side records that grant
// AI agent callers (CallerAIAgent) permission to mint installation
// tokens via the broker. Implementation is Phase 2c-2 (Firestore-
// backed for Cloud Run multi-instance safety per plan v8 §5.2).
//
// Multi-instance contract: an instance that calls Register MUST be
// observable from any other instance's Get; in-process state is
// not sufficient. The Phase 2c-2 Firestore impl satisfies this; an
// in-memory dev impl exists only for unit tests.
type AgentSessionRegistry interface {
	// Register stores a fresh AgentSession (typically produced by
	// domain.NewAgentSession). Returns an error if the SessionID
	// already exists — the caller should retry with a new session
	// rather than overwriting (idempotent registration is not
	// supported by design; the workspace daemon owns the lifecycle).
	Register(ctx context.Context, sess domain.AgentSession) error
	// Get returns the AgentSession for sessionID. Returns
	// domain.ErrAgentSessionNotFound when the session does not
	// exist (revoked sessions are still returned with RevokedAt
	// set so the broker's verify path can render the right
	// audit signal).
	Get(ctx context.Context, sessionID string) (domain.AgentSession, error)
	// Revoke marks the session as revoked by setting RevokedAt.
	// Idempotent: revoking an already-revoked session must not
	// error. Revoking a missing session returns
	// domain.ErrAgentSessionNotFound.
	Revoke(ctx context.Context, sessionID string) error
}
