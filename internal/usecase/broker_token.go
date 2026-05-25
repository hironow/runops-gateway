package usecase

import (
	"context"
	"errors"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Sentinel errors raised by BrokerTokenService.Mint that do NOT
// originate from domain (= grant matrix violations) or registry
// (= ErrProjectNotFound). Each maps to a distinct caller-visible
// 4xx in the HTTP layer.
var (
	// ErrProjectInstallationMissing is returned when the project is
	// registered but has no GitHub App installation bound. Distinct
	// from domain.ErrProjectNotFound so the HTTP handler can render
	// "no app installed for this project" vs "project unknown".
	ErrProjectInstallationMissing = errors.New("usecase: project has no GitHub App installation bound")
	// ErrProjectNotActive is returned when the project lifecycle has
	// stopped (archived / paused / deleted). Per plan v8 §5.3 codex
	// review v7 #2, the broker MUST refuse archived projects even
	// when every other gate would have passed.
	ErrProjectNotActive = errors.New("usecase: project status is not active")
)

// BrokerTokenService orchestrates a GitHub installation token mint
// (refs#0007 plan v8 §6 Phase 1b). It is the primary use-case-layer
// entry point that wires the verified caller credential
// (domain.BrokerActor) into the grant decision (domain.GrantPolicy),
// the per-project repo binding lookup (port.ProjectRegistry), and
// the actual mint call (port.GitHubTokenBroker).
//
// Per plan v8 §5.4 schema lockdown, callers may NOT self-claim the
// actor — the HTTP handler MUST construct domain.BrokerActor from a
// verified inbound credential before calling Mint. Likewise, the
// raw caller request body MUST have passed
// domain.ValidateBrokerRequest at the HTTP boundary; this service
// trusts that BrokerRequest.Tool / ProjectID / SessionID arrived
// without hidden escalation fields.
type BrokerTokenService struct {
	policy   domain.GrantPolicy
	registry port.ProjectRegistry
	broker   port.GitHubTokenBroker
}

// NewBrokerTokenService wires the three dependencies the orchestration
// needs. Each is an interface defined in core/domain or core/port so
// the use case stays free of infrastructure imports.
func NewBrokerTokenService(policy domain.GrantPolicy, registry port.ProjectRegistry, broker port.GitHubTokenBroker) *BrokerTokenService {
	return &BrokerTokenService{policy: policy, registry: registry, broker: broker}
}

// Mint executes the token-broker request pipeline:
//
//  1. Apply the grant matrix (domain.GrantPolicy.IsAllowed).
//     Phonewave is denied for every caller type, so this short-circuits
//     before any registry / broker work happens.
//  2. (Phase 1b: registry lookup + active-status check land here in
//     subsequent commits.)
//  3. (Phase 2: actual broker.Mint call lands here once the GitHub App
//     adapter exists.)
//
// Phase 1b: grant matrix + per-project repo binding + active-status
// gate are wired here. Phase 2 will replace the broker.Mint call
// with the real GitHub App adapter.
func (s *BrokerTokenService) Mint(ctx context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error) {
	if err := s.policy.IsAllowed(actor.Type, req.Tool); err != nil {
		return domain.InstallationToken{}, err
	}
	project, err := s.registry.Get(ctx, req.ProjectID)
	if err != nil {
		return domain.InstallationToken{}, err
	}
	if project.Status != domain.ProjectStatusActive {
		return domain.InstallationToken{}, ErrProjectNotActive
	}
	if project.GitHubAppInstallationID == 0 {
		return domain.InstallationToken{}, ErrProjectInstallationMissing
	}
	return s.broker.Mint(ctx, req, actor)
}
