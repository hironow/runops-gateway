// Package port — token broker secondary port (plan v8 §6 step 11).
//
// This file is intentionally a separate compilation unit from port.go
// because ADR 0033's release-gate path classifier matches the glob
// `internal/core/port/*token*` to escalate any change here to the
// `auth_boundary` change category. Keeping the broker port out of the
// generic port.go means routine edits to the existing ports are not
// dragged through the meta-gate, while every change to the token
// boundary is surfaced for two-reviewer review.
package port

import (
	"context"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// BrokerRequest is the broker's INTERNAL request shape — built by the
// HTTP handler AFTER plan v8 §5.4 schema lockdown has stripped any
// caller-supplied escalation fields. Only fields here may flow into
// the use-case layer.
//
// SessionID is populated only when the verified caller is an AI agent
// (CallerAIAgent); for the other three caller types it is the empty
// string and the broker treats a non-empty value as a contract bug.
type BrokerRequest struct {
	ProjectID string
	Tool      domain.Tool
	SessionID string
}

// GitHubTokenBroker is the secondary port that mints a short-lived
// GitHub installation token bound to one (project_id, tool, actor)
// triple. Implementations live in
// `internal/adapter/output/github/installation_token_broker.go`
// (Phase 2) and resolve the project's installation_id + repo binding
// via the existing ProjectRegistry, then call the GitHub App
// installation token mint API with the per-tool permission scope
// from domain.GrantPolicy.PermissionsFor.
//
// The actor argument is broker-derived (verified-credential output)
// and travels into the use-case layer via this port; callers may NOT
// self-claim it (plan v8 §5.4 schema lockdown).
type GitHubTokenBroker interface {
	Mint(ctx context.Context, req BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error)
}
