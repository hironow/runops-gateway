// Package github implements port.GitHubTokenBroker against the
// real GitHub App installation token mint API (refs#0007 plan v8
// §6 step 14). Phase 2b-1 (this commit) ships the orchestration:
// project lookup, grant-matrix permission translation, per-project
// repo binding (single-repo only — plan v8 §5.3), and response
// construction including AuditFingerprint. The actual GitHub HTTP
// call is hidden behind the Minter interface so
// the orchestration is unit-testable; Phase 2b-2 plugs in
// ghinstallation/v2 + go-github's API client.
package github

import (
	"context"

	gogh "github.com/google/go-github/v84/github"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Minter is the seam between the broker orchestration (this file)
// and the actual GitHub App installation token mint call. The
// production implementation is *GhinstallationMinter (Phase 2b-2-1)
// wrapping ghinstallation/v2 + gogh.AppsService.CreateInstallationToken.
//
// Exported in Phase 3b-3b-1 so the composition root can build an
// *InstallationTokenBroker without re-implementing the minter
// interface in another package.
type Minter interface {
	Mint(ctx context.Context, installationID int64, opts *gogh.InstallationTokenOptions) (*gogh.InstallationToken, error)
}

// InstallationTokenBroker implements port.GitHubTokenBroker.
//
// The broker is the LAST line of defence on per-project repo
// binding (plan v8 §5.3): even if the use case were bypassed, the
// broker still resolves the project's owner/repo from the registry
// and pins the mint request to that single repository. paintress
// global / cross-project / multi-repo tokens cannot be produced.
type InstallationTokenBroker struct {
	minter   Minter
	registry port.ProjectRegistry
	policy   domain.GrantPolicy
}

// NewInstallationTokenBroker wires the dependencies the orchestration
// needs. The Minter argument lets the composition root inject either
// the production *GhinstallationMinter or a test fake; the broker
// itself stays free of GitHub HTTP / JWT concerns.
func NewInstallationTokenBroker(minter Minter, registry port.ProjectRegistry, policy domain.GrantPolicy) *InstallationTokenBroker {
	return &InstallationTokenBroker{minter: minter, registry: registry, policy: policy}
}

// Mint resolves the project's installation_id + repo binding,
// translates the per-tool grant-matrix permissions to the GitHub
// API shape, calls the upstream mint API via Minter, and
// returns a domain.InstallationToken whose AuditFingerprint is
// already computed (so log / OTel surfaces never see the raw
// token; plan v8 §5.5 leakage policy).
func (b *InstallationTokenBroker) Mint(ctx context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error) {
	perms, err := b.policy.PermissionsFor(req.Tool)
	if err != nil {
		return domain.InstallationToken{}, err
	}
	project, err := b.registry.Get(ctx, req.ProjectID)
	if err != nil {
		return domain.InstallationToken{}, err
	}
	opts := &gogh.InstallationTokenOptions{
		Repositories: []string{project.GitHubRepo},
		Permissions:  translatePermissions(perms),
	}
	ghTok, err := b.minter.Mint(ctx, project.GitHubAppInstallationID, opts)
	if err != nil {
		return domain.InstallationToken{}, err
	}
	rawToken := ghTok.GetToken()
	return domain.InstallationToken{
		Token:            rawToken,
		ExpiresAt:        ghTok.GetExpiresAt().Time,
		Actor:            actor,
		ProjectID:        req.ProjectID,
		Tool:             req.Tool,
		Permissions:      perms,
		AuditFingerprint: domain.AuditFingerprint(rawToken),
	}, nil
}

// translatePermissions maps the broker's grant-matrix permissions
// (read / write / none) to the GitHub API shape (string pointers
// inside InstallationPermissions). PermNone fields are left as
// nil pointers so the GitHub API treats them as unset, narrowing
// the token's actual scope to exactly what the matrix grants.
func translatePermissions(p domain.RepositoryPermissions) *gogh.InstallationPermissions {
	out := &gogh.InstallationPermissions{}
	if p.Contents != domain.PermNone {
		s := string(p.Contents)
		out.Contents = &s
	}
	if p.PullRequests != domain.PermNone {
		s := string(p.PullRequests)
		out.PullRequests = &s
	}
	return out
}
