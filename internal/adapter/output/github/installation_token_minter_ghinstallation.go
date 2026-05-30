package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gogh "github.com/google/go-github/v84/github"
)

// GhinstallationMinter is the production Minter (Phase 2b-2-1).
// It uses bradleyfalzon/ghinstallation/v2 to:
//
//  1. Construct a GitHub App JWT signed by the App's private key
//     (RS256, ~10 minute expiry per GitHub spec).
//  2. Exchange the JWT for an installation access token via the
//     GitHub API endpoint POST /app/installations/{id}/access_tokens.
//     The endpoint scopes the resulting token to the requested
//     Repositories + Permissions.
//
// The struct satisfies the unexported `Minter` interface used
// by InstallationTokenBroker (Phase 2b-1, PR #59); production wiring
// in Phase 3b composition root will inject this concrete type via
// `NewInstallationTokenBroker`.
//
// Phase 2b-2-1 ships the wiring + ctor failure paths only — the
// real GitHub API call lands in Phase 3c integration tests with
// a test GitHub App secret. The unit-testable cryptography is
// fully delegated to ghinstallation/v2 (= secure-by-default lib).
type GhinstallationMinter struct {
	appID         int64
	privateKeyPEM []byte
	baseURL       string
	httpClient    *http.Client
}

// NewGhinstallationMinter validates the inputs at ctor time so a
// misconfigured deployment fails at startup rather than on the
// first inbound broker request. nil http.Client falls back to the
// stdlib default (suitable when no custom transport is needed).
//
// baseURL overrides the GitHub API endpoint (default
// https://api.github.com). Empty string keeps the default; set it to
// point at a local emulator (e.g. emulate http://localhost:4100) for
// offline broker verification.
func NewGhinstallationMinter(appID int64, privateKeyPEM []byte, baseURL string, client *http.Client) (*GhinstallationMinter, error) {
	if appID <= 0 {
		return nil, ErrGhinstallationInvalidAppID
	}
	if len(privateKeyPEM) == 0 {
		return nil, ErrGhinstallationMissingPrivateKey
	}
	// Probe the private key shape early. ghinstallation parses the
	// key on every transport construction, but we want the failure
	// surface at startup.
	if _, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, privateKeyPEM); err != nil {
		return nil, fmt.Errorf("ghinstallation: invalid app private key: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &GhinstallationMinter{
		appID:         appID,
		privateKeyPEM: privateKeyPEM,
		baseURL:       baseURL,
		httpClient:    client,
	}, nil
}

// Mint exchanges the GitHub App JWT for an installation token
// scoped to (installationID, opts.Repositories, opts.Permissions).
// The returned *gogh.InstallationToken carries the raw GHS token
// in Token + the GitHub-assigned ExpiresAt (typically 1h).
//
// Errors from the GitHub API surface unchanged so the broker can
// distinguish 404 (installation not found) from 422 (permissions
// not in matrix) etc. — the use case / handler then renders the
// right HTTP status to the caller.
func (m *GhinstallationMinter) Mint(ctx context.Context, installationID int64, opts *gogh.InstallationTokenOptions) (*gogh.InstallationToken, error) {
	// http.DefaultClient.Transport is nil; ghinstallation's AppsTransport
	// dereferences the supplied RoundTripper in RoundTrip and would panic.
	// Fall back to http.DefaultTransport so a nil-Transport client (the
	// common production case from NewBrokerDependencies(..., nil)) works.
	baseRT := m.httpClient.Transport
	if baseRT == nil {
		baseRT = http.DefaultTransport
	}
	transport, err := ghinstallation.NewAppsTransport(baseRT, m.appID, m.privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("ghinstallation: build apps transport: %w", err)
	}
	client := gogh.NewClient(&http.Client{Transport: transport})
	// baseURL override (e.g. local emulator). Empty keeps go-github's
	// api.github.com default. WithEnterpriseURLs is intentionally avoided
	// here: it appends "/api/v3/" for non-"api."-prefixed hosts, which does
	// not match the GitHub.com-shaped emulator endpoints. Set BaseURL
	// directly instead (go-github requires a trailing slash).
	if m.baseURL != "" {
		base, perr := url.Parse(strings.TrimRight(m.baseURL, "/") + "/")
		if perr != nil {
			return nil, fmt.Errorf("ghinstallation: parse base URL %q: %w", m.baseURL, perr)
		}
		client.BaseURL = base
	}
	tok, _, err := client.Apps.CreateInstallationToken(ctx, installationID, opts)
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// Sentinel errors raised by NewGhinstallationMinter at ctor time.
// Each maps to a distinct startup-failure audit signal.
var (
	ErrGhinstallationInvalidAppID      = errors.New("ghinstallation: app_id must be positive")
	ErrGhinstallationMissingPrivateKey = errors.New("ghinstallation: private key PEM must be non-empty")
)

// Compile-time assertion that GhinstallationMinter satisfies the
// unexported Minter interface used by InstallationTokenBroker.
var _ Minter = (*GhinstallationMinter)(nil)
