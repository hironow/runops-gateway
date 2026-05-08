package composition

import (
	"context"
	"errors"
	"fmt"

	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	"github.com/hironow/runops-gateway/internal/adapter/output/cache"
	githubadapter "github.com/hironow/runops-gateway/internal/adapter/output/github"
	"github.com/hironow/runops-gateway/internal/adapter/output/registry"
	"github.com/hironow/runops-gateway/internal/adapter/output/secret"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

// BrokerDependencies bundles the dependencies the broker HTTP
// handler (internal/adapter/input/broker/handler.go) needs
// (refs#0007 plan v8 §6 step 17 Phase 3b-3b-1). cmd/server/main.go
// (Phase 3b-3b-2) constructs a single BrokerDependencies via
// NewBrokerDependencies and passes Service + Authenticator into
// broker.NewHandler.
type BrokerDependencies struct {
	// Service is the use-case-layer mint orchestrator. It satisfies
	// broker.BrokerService via Go's structural typing.
	Service *usecase.BrokerTokenService
	// Authenticator dispatches the inbound request to one of the
	// 4 caller-type verifiers. It satisfies broker.Authenticator
	// via structural typing.
	Authenticator *auth.ChainAuthenticator
	// AgentSessionRegistry is exposed so admin tooling (= the
	// /broker/agent-sessions endpoint planned for after Phase 3b)
	// can call Register / Revoke without re-resolving the
	// dependency graph.
	AgentSessionRegistry port.AgentSessionRegistry
}

// NewBrokerDependencies wires every broker-side dependency from the
// resolved BrokerConfig + the externally-supplied ProjectRegistry
// (the project registry already exists in the gateway and is
// shared with the rest of the app).
//
// The function reads the GitHub App private key from disk because
// Phase 2b-2-2 (Secret Manager fetch) has not yet shipped; once
// it does, the read will move behind a fetcher port.
func NewBrokerDependencies(ctx context.Context, cfg *BrokerConfig, projectRegistry port.ProjectRegistry) (*BrokerDependencies, error) {
	if cfg == nil {
		return nil, ErrBrokerDependenciesNilConfig
	}
	if projectRegistry == nil {
		return nil, ErrBrokerDependenciesNilProjectRegistry
	}

	keyFetcher, err := newPrivateKeyFetcher(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("composition: build private key fetcher: %w", err)
	}
	keyPEM, err := keyFetcher.Fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("composition: fetch GitHub App private key: %w", err)
	}

	minter, err := githubadapter.NewGhinstallationMinter(cfg.GitHubAppID, keyPEM, nil)
	if err != nil {
		return nil, fmt.Errorf("composition: build ghinstallation minter: %w", err)
	}

	jwks, err := auth.NewJWKSVerifier(ctx, cfg.GoogleJWKSURL, cfg.Audience, nil)
	if err != nil {
		return nil, fmt.Errorf("composition: build JWKS verifier: %w", err)
	}

	gcloudVerifier := auth.NewGcloudIdentityTokenVerifier(jwks, cfg.GoogleSTSIssuer, cfg.OperatorEmails)
	cloudrunVerifier, err := auth.NewCloudRunIAMVerifier(jwks, cfg.GoogleSTSIssuer, cfg.GatewayServiceSAs)
	if err != nil {
		return nil, fmt.Errorf("composition: build cloudrun_iam verifier: %w", err)
	}
	workloadVerifier, err := auth.NewWorkloadIdentityVerifier(jwks, cfg.GoogleSTSIssuer, cfg.WorkspaceDaemonSAs)
	if err != nil {
		return nil, fmt.Errorf("composition: build workload_identity verifier: %w", err)
	}

	// Phase 2c-2-2 will replace the in-memory registry with the
	// Firestore impl when cfg.UseFirestoreRegistry is true. Until
	// then every deployment shares the in-memory variant — fine
	// for dev / single-instance Cloud Run but unsafe for
	// multi-instance (the Phase 2c-1 port comment documents this).
	agentRegistry := registry.NewInMemoryAgentSessionRegistry()
	delegatedVerifier := auth.NewDelegatedAgentVerifier(cfg.Audience, agentRegistry, nil)

	chain := auth.NewChainAuthenticator(gcloudVerifier, cloudrunVerifier, workloadVerifier, delegatedVerifier)

	brokerImpl := githubadapter.NewInstallationTokenBroker(minter, projectRegistry, domain.DefaultGrantPolicy())
	cachedImpl := newCachedBroker(brokerImpl)

	tokenService := usecase.NewBrokerTokenService(domain.DefaultGrantPolicy(), projectRegistry, cachedImpl)

	return &BrokerDependencies{
		Service:              tokenService,
		Authenticator:        chain,
		AgentSessionRegistry: agentRegistry,
	}, nil
}

// newPrivateKeyFetcher selects between the file-based and Secret
// Manager-based fetcher per the BrokerConfig env-var split (Phase
// 2b-2-2). LoadBrokerConfig already enforces "exactly one source",
// so this function asserts the same invariant defensively.
func newPrivateKeyFetcher(ctx context.Context, cfg *BrokerConfig) (secret.PrivateKeyFetcher, error) {
	switch {
	case cfg.GitHubAppPrivateKeySecretName != "":
		return secret.NewSecretManagerPrivateKeyFetcher(ctx, cfg.GitHubAppPrivateKeySecretName)
	case cfg.GitHubAppPrivateKeyPath != "":
		return secret.NewFilePrivateKeyFetcher(cfg.GitHubAppPrivateKeyPath)
	default:
		return nil, ErrBrokerDependenciesPrivateKeySourceMissing
	}
}

// cachedBroker decorates *githubadapter.InstallationTokenBroker with
// the Phase 2a in-memory token cache. Same (project_id, tool,
// actor.type, actor.user_email) tuples produce the same cache key,
// so concurrent mints for the same caller-target collapse to a
// single upstream API call (singleflight inside the cache).
type cachedBroker struct {
	inner *githubadapter.InstallationTokenBroker
	cache *cache.InstallationTokenCache
}

func newCachedBroker(inner *githubadapter.InstallationTokenBroker) *cachedBroker {
	return &cachedBroker{inner: inner, cache: cache.NewInstallationTokenCache()}
}

func (c *cachedBroker) Mint(ctx context.Context, req port.BrokerRequest, actor domain.BrokerActor) (domain.InstallationToken, error) {
	key := cacheKey(req, actor)
	return c.cache.GetOrFetch(ctx, key, func(ctx context.Context) (domain.InstallationToken, error) {
		return c.inner.Mint(ctx, req, actor)
	})
}

// cacheKey scopes the cache to the (project, tool, caller-type,
// caller-email) tuple. SessionID is intentionally NOT in the key:
// AI-agent sessions for the same (project, tool) share the cached
// token, and per-session boundary is enforced at the verifier
// layer (DelegatedAgentVerifier rejects mismatched sessions BEFORE
// the use case is even reached).
func cacheKey(req port.BrokerRequest, actor domain.BrokerActor) string {
	return fmt.Sprintf("%s|%s|%s|%s", req.ProjectID, req.Tool, actor.Type, actor.UserEmail)
}

// Sentinel errors raised by NewBrokerDependencies.
var (
	ErrBrokerDependenciesNilConfig               = errors.New("composition: BrokerConfig must be non-nil")
	ErrBrokerDependenciesNilProjectRegistry      = errors.New("composition: ProjectRegistry must be non-nil")
	ErrBrokerDependenciesPrivateKeySourceMissing = errors.New("composition: neither GitHubAppPrivateKeyPath nor GitHubAppPrivateKeySecretName is set")
)
