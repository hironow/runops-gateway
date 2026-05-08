// Package composition holds env-var-driven configuration that the
// cmd/server composition root translates into broker dependencies
// (refs#0007 plan v8 §6 step 17 Phase 3b-3a).
//
// The package is intentionally infrastructure-free: it only reads
// strings from os.Getenv and produces a typed Config + sentinel
// errors. The actual wiring of *JWKSVerifier / *ChainAuthenticator /
// *BrokerTokenService / cache / minter / registry happens in
// cmd/server/main.go (Phase 3b-3b).
package composition

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// BrokerConfig is the resolved broker configuration. Every field
// has been validated by LoadBrokerConfig — production callers can
// trust the values without re-checking.
type BrokerConfig struct {
	// Audience is the broker URL that all caller identity tokens
	// must declare in their `aud` claim. Pinned by every verifier.
	Audience string
	// GoogleSTSIssuer is the issuer URL all 4 caller types use
	// (defaults to "https://accounts.google.com" if unset).
	GoogleSTSIssuer string
	// GoogleJWKSURL is the URL the JWKsVerifier hits to fetch
	// public keys. Defaults to Google STS's well-known endpoint
	// if unset.
	GoogleJWKSURL string
	// OperatorEmails is the human-operator allowlist. Empty (= no
	// enforcement) is permitted ONLY for bootstrap / dev; production
	// deployments should always populate this. The gcloud_identity
	// verifier accepts empty slice as "any verified caller".
	OperatorEmails []string
	// GatewayServiceSAs is the allowlist of Cloud Run service-account
	// emails permitted to act as gateway-service callers. REQUIRED
	// — empty causes LoadBrokerConfig to fail (cloudrun_iam verifier
	// rejects empty allowlist at ctor time).
	GatewayServiceSAs []string
	// WorkspaceDaemonSAs is the allowlist of GCE workload-identity
	// service-account emails. REQUIRED — empty causes LoadBrokerConfig
	// to fail (workload_identity verifier rejects empty allowlist).
	WorkspaceDaemonSAs []string
	// GitHubAppID is the numeric GitHub App identifier.
	GitHubAppID int64
	// GitHubAppPrivateKeyPath points at a PEM-encoded RSA private
	// key on disk. Phase 2b-2-2 will add a Secret Manager
	// alternative; for Phase 3b-3a, the file path is the only
	// supported sourcing mode.
	GitHubAppPrivateKeyPath string
	// UseFirestoreRegistry selects between the Firestore-backed
	// agent session registry (production) and the in-memory
	// registry (dev / staging). Default is in-memory until Phase
	// 2c-2-2 ships the Firestore impl.
	UseFirestoreRegistry bool
}

// LoadBrokerConfig reads the broker env vars, validates them, and
// returns the resolved Config. Each missing / invalid env var
// produces its own sentinel so the composition root can render
// a precise startup-failure message.
func LoadBrokerConfig() (*BrokerConfig, error) {
	audience := strings.TrimSpace(os.Getenv("BROKER_AUDIENCE"))
	if audience == "" {
		return nil, ErrBrokerConfigMissingAudience
	}

	issuer := strings.TrimSpace(os.Getenv("GOOGLE_STS_ISSUER"))
	if issuer == "" {
		issuer = defaultGoogleSTSIssuer
	}

	jwksURL := strings.TrimSpace(os.Getenv("GOOGLE_JWKS_URL"))
	if jwksURL == "" {
		jwksURL = defaultGoogleJWKSURL
	}

	gatewaySAs := parseCSVNonEmpty(os.Getenv("BROKER_GATEWAY_SERVICE_SAS"))
	if len(gatewaySAs) == 0 {
		return nil, ErrBrokerConfigMissingGatewayServiceSAs
	}

	workspaceSAs := parseCSVNonEmpty(os.Getenv("BROKER_WORKSPACE_DAEMON_SAS"))
	if len(workspaceSAs) == 0 {
		return nil, ErrBrokerConfigMissingWorkspaceDaemonSAs
	}

	appIDRaw := strings.TrimSpace(os.Getenv("GITHUB_APP_ID"))
	if appIDRaw == "" {
		return nil, ErrBrokerConfigMissingGitHubAppID
	}
	appID, err := strconv.ParseInt(appIDRaw, 10, 64)
	if err != nil || appID <= 0 {
		return nil, fmt.Errorf("%w: %q", ErrBrokerConfigInvalidGitHubAppID, appIDRaw)
	}

	keyPath := strings.TrimSpace(os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"))
	if keyPath == "" {
		return nil, ErrBrokerConfigMissingPrivateKeyPath
	}

	return &BrokerConfig{
		Audience:                audience,
		GoogleSTSIssuer:         issuer,
		GoogleJWKSURL:           jwksURL,
		OperatorEmails:          parseCSVNonEmpty(os.Getenv("BROKER_OPERATOR_EMAILS")),
		GatewayServiceSAs:       gatewaySAs,
		WorkspaceDaemonSAs:      workspaceSAs,
		GitHubAppID:             appID,
		GitHubAppPrivateKeyPath: keyPath,
		UseFirestoreRegistry:    parseBool(os.Getenv("BROKER_USE_FIRESTORE_REGISTRY")),
	}, nil
}

const (
	defaultGoogleSTSIssuer = "https://accounts.google.com"
	defaultGoogleJWKSURL   = "https://www.googleapis.com/oauth2/v3/certs"
)

// parseCSVNonEmpty splits raw on "," and drops empty / whitespace
// entries so a stray trailing comma does not produce a phantom "".
func parseCSVNonEmpty(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseBool accepts "true" / "1" (case-insensitive) as true; every
// other value (including the empty string) is false.
func parseBool(raw string) bool {
	s := strings.ToLower(strings.TrimSpace(raw))
	return s == "true" || s == "1"
}

// Sentinel errors raised by LoadBrokerConfig. Each maps to a
// specific env-var failure mode so the composition root can render
// a precise startup-failure message.
var (
	ErrBrokerConfigMissingAudience           = errors.New("composition: BROKER_AUDIENCE is required")
	ErrBrokerConfigMissingGatewayServiceSAs  = errors.New("composition: BROKER_GATEWAY_SERVICE_SAS must be a non-empty CSV")
	ErrBrokerConfigMissingWorkspaceDaemonSAs = errors.New("composition: BROKER_WORKSPACE_DAEMON_SAS must be a non-empty CSV")
	ErrBrokerConfigMissingGitHubAppID        = errors.New("composition: GITHUB_APP_ID is required")
	ErrBrokerConfigInvalidGitHubAppID        = errors.New("composition: GITHUB_APP_ID must be a positive integer")
	ErrBrokerConfigMissingPrivateKeyPath     = errors.New("composition: GITHUB_APP_PRIVATE_KEY_PATH is required")
)
