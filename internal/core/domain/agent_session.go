package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// AgentSession is the broker-side record that grants an AI agent
// (CallerAIAgent) permission to mint installation tokens for a
// specific (project_id, tool) pair on behalf of a workspace daemon
// service account (plan v8 §5.2).
//
// The session is created once by the workspace daemon at expedition
// start (Register) and verified on every subsequent broker call
// (Verify). The (workspace_daemon_sa, project_id, tool) triple is
// pinned at Register time — verification fails if any of the three
// drifts away from the registered value, even if the SessionID is
// otherwise valid.
type AgentSession struct {
	SessionID         string
	WorkspaceDaemonSA string
	ProjectID         string
	Tool              Tool
	IssuedAt          time.Time
	ExpiresAt         time.Time
	// RevokedAt is non-nil when the session has been explicitly
	// revoked. Any non-nil value (regardless of whether it is in
	// the past or future) means "revoked" — the field stores the
	// audit timestamp, not a future-effective revocation date.
	RevokedAt *time.Time
}

// NewAgentSession returns a fresh session with a random 32-char
// lowercase-hex SessionID, IssuedAt = now, ExpiresAt = now + ttl,
// and RevokedAt = nil. Inputs are validated at the boundary so
// the registry never persists garbage.
func NewAgentSession(workspaceDaemonSA, projectID string, tool Tool, ttl time.Duration, now time.Time) (AgentSession, error) {
	if workspaceDaemonSA == "" {
		return AgentSession{}, fmt.Errorf("agent_session: workspace_daemon_sa is required")
	}
	if projectID == "" {
		return AgentSession{}, fmt.Errorf("agent_session: project_id is required")
	}
	if _, err := DefaultGrantPolicy().PermissionsFor(tool); err != nil {
		return AgentSession{}, fmt.Errorf("agent_session: tool %q not permitted: %w", tool, err)
	}
	if ttl <= 0 {
		return AgentSession{}, fmt.Errorf("agent_session: ttl must be positive, got %v", ttl)
	}
	id, err := newSessionID()
	if err != nil {
		return AgentSession{}, fmt.Errorf("agent_session: cannot generate session id: %w", err)
	}
	return AgentSession{
		SessionID:         id,
		WorkspaceDaemonSA: workspaceDaemonSA,
		ProjectID:         projectID,
		Tool:              tool,
		IssuedAt:          now,
		ExpiresAt:         now.Add(ttl),
	}, nil
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// VerifyAgentSession returns nil when the session is still valid
// for the (claimedSA, projectID, tool) triple as of `now`. Each
// failure mode produces its own sentinel so the broker can audit
// what kind of attempt was rejected — SA mismatch / project
// mismatch / tool mismatch are attack-shaped, while expired /
// revoked are routine lifecycle events.
//
// The check ORDER matters: revoked / expired are evaluated BEFORE
// the identity checks so an attacker probing for revoked sessions
// cannot use the identity-mismatch sentinel as a signal of
// session existence.
func VerifyAgentSession(sess AgentSession, claimedSA, projectID string, tool Tool, now time.Time) error {
	if sess.RevokedAt != nil {
		return ErrAgentSessionRevoked
	}
	if !now.Before(sess.ExpiresAt) {
		return ErrAgentSessionExpired
	}
	if sess.WorkspaceDaemonSA != claimedSA {
		return ErrAgentSessionSAMismatch
	}
	if sess.ProjectID != projectID {
		return ErrAgentSessionProjectMismatch
	}
	if sess.Tool != tool {
		return ErrAgentSessionToolMismatch
	}
	return nil
}

// Sentinel errors raised by AgentSession verification. Each maps to
// a distinct audit category so the broker can distinguish lifecycle
// events (expired / revoked) from attack-shaped attempts (SA /
// project / tool mismatch).
var (
	ErrAgentSessionRevoked         = errors.New("agent_session: revoked")
	ErrAgentSessionExpired         = errors.New("agent_session: expired")
	ErrAgentSessionSAMismatch      = errors.New("agent_session: workspace daemon SA mismatch")
	ErrAgentSessionProjectMismatch = errors.New("agent_session: project_id mismatch")
	ErrAgentSessionToolMismatch    = errors.New("agent_session: tool mismatch")
	ErrAgentSessionNotFound        = errors.New("agent_session: not found in registry")
)
