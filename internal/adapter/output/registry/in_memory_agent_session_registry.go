// Package registry holds the AI-agent session registry adapters
// (refs#0007 plan v8 §5.2 / §6 step 13).
//
// Phase 2c-1 (PR #60) shipped the domain types + port interface;
// Phase 2c-2-1 (this file) ships the in-memory implementation
// suitable for local development, unit tests, and the Phase 3b
// composition root's bootstrap config. Phase 2c-2-2 will add the
// Firestore-backed implementation that satisfies the Cloud Run
// multi-instance contract documented in
// `internal/core/port/agent_session_registry.go`.
package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// InMemoryAgentSessionRegistry stores sessions in a goroutine-safe
// map. It does NOT satisfy the Cloud Run multi-instance contract
// — instances do not share state. Use only for local development
// and tests.
type InMemoryAgentSessionRegistry struct {
	mu       sync.Mutex
	sessions map[string]domain.AgentSession
}

// NewInMemoryAgentSessionRegistry returns an empty registry.
func NewInMemoryAgentSessionRegistry() *InMemoryAgentSessionRegistry {
	return &InMemoryAgentSessionRegistry{sessions: make(map[string]domain.AgentSession)}
}

// Register stores sess. Returns an error when SessionID is already
// present — the workspace daemon owns the lifecycle, so idempotent
// registration would mask concurrent-create bugs.
func (r *InMemoryAgentSessionRegistry) Register(_ context.Context, sess domain.AgentSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[sess.SessionID]; ok {
		return fmt.Errorf("agent_session: SessionID %q already registered", sess.SessionID)
	}
	r.sessions[sess.SessionID] = sess
	return nil
}

// Get returns the session with sessionID. Returns
// domain.ErrAgentSessionNotFound when the session has never been
// registered. Revoked sessions are still returned (with RevokedAt
// non-nil) so the verifier can render the "revoked" audit signal.
func (r *InMemoryAgentSessionRegistry) Get(_ context.Context, sessionID string) (domain.AgentSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return domain.AgentSession{}, domain.ErrAgentSessionNotFound
	}
	return s, nil
}

// Revoke marks the session as revoked. Idempotent: revoking an
// already-revoked session is a no-op success. Returns
// domain.ErrAgentSessionNotFound for unknown sessionIDs so admin
// tooling can distinguish "missing" from "already revoked".
func (r *InMemoryAgentSessionRegistry) Revoke(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sessionID]
	if !ok {
		return domain.ErrAgentSessionNotFound
	}
	if s.RevokedAt != nil {
		// Idempotent: do not overwrite the original revocation
		// timestamp on a second call.
		return nil
	}
	now := time.Now()
	s.RevokedAt = &now
	r.sessions[sessionID] = s
	return nil
}
