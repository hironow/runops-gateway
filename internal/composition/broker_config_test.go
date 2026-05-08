package composition_test

import (
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/composition"
)

// envSet sets every key from m for the test's duration via t.Setenv,
// which Go automatically reverts at test end.
func envSet(t *testing.T, m map[string]string) {
	t.Helper()
	for k, v := range m {
		t.Setenv(k, v)
	}
}

// happyEnv is the minimum env set that LoadBrokerConfig accepts.
func happyEnv() map[string]string {
	return map[string]string{
		"BROKER_AUDIENCE":             "https://broker.example.com",
		"BROKER_GATEWAY_SERVICE_SAS":  "gateway-internal@example.iam.gserviceaccount.com",
		"BROKER_WORKSPACE_DAEMON_SAS": "workspace-daemon@example.iam.gserviceaccount.com",
		"GITHUB_APP_ID":               "12345",
		"GITHUB_APP_PRIVATE_KEY_PATH": "/etc/secrets/github-app.pem",
	}
}

// Happy path: the minimum env set produces a fully populated config
// with sensible defaults for the optional fields.
func TestLoadBrokerConfig_HappyPath(t *testing.T) {
	envSet(t, happyEnv())
	cfg, err := composition.LoadBrokerConfig()
	if err != nil {
		t.Fatalf("LoadBrokerConfig: %v", err)
	}
	if cfg.Audience != "https://broker.example.com" {
		t.Errorf("Audience = %q", cfg.Audience)
	}
	if cfg.GoogleSTSIssuer != "https://accounts.google.com" {
		t.Errorf("GoogleSTSIssuer default = %q", cfg.GoogleSTSIssuer)
	}
	if cfg.GoogleJWKSURL != "https://www.googleapis.com/oauth2/v3/certs" {
		t.Errorf("GoogleJWKSURL default = %q", cfg.GoogleJWKSURL)
	}
	if cfg.GitHubAppID != 12345 {
		t.Errorf("GitHubAppID = %d", cfg.GitHubAppID)
	}
	if cfg.UseFirestoreRegistry {
		t.Errorf("UseFirestoreRegistry must default to false")
	}
	if cfg.OperatorEmails != nil {
		t.Errorf("OperatorEmails default must be nil (= no allowlist), got %v", cfg.OperatorEmails)
	}
	if len(cfg.GatewayServiceSAs) != 1 || cfg.GatewayServiceSAs[0] != "gateway-internal@example.iam.gserviceaccount.com" {
		t.Errorf("GatewayServiceSAs = %v", cfg.GatewayServiceSAs)
	}
}

// Each required env var has its own sentinel so the startup
// failure message is precise.
func TestLoadBrokerConfig_RejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]struct {
		removeKey    string
		wantSentinel error
	}{
		"missing audience":         {removeKey: "BROKER_AUDIENCE", wantSentinel: composition.ErrBrokerConfigMissingAudience},
		"missing gateway SAs":      {removeKey: "BROKER_GATEWAY_SERVICE_SAS", wantSentinel: composition.ErrBrokerConfigMissingGatewayServiceSAs},
		"missing workspace SAs":    {removeKey: "BROKER_WORKSPACE_DAEMON_SAS", wantSentinel: composition.ErrBrokerConfigMissingWorkspaceDaemonSAs},
		"missing app id":           {removeKey: "GITHUB_APP_ID", wantSentinel: composition.ErrBrokerConfigMissingGitHubAppID},
		"missing private key path": {removeKey: "GITHUB_APP_PRIVATE_KEY_PATH", wantSentinel: composition.ErrBrokerConfigMissingPrivateKeyPath},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := happyEnv()
			delete(env, c.removeKey)
			envSet(t, env)
			t.Setenv(c.removeKey, "")
			_, err := composition.LoadBrokerConfig()
			if !errors.Is(err, c.wantSentinel) {
				t.Errorf("[%s] want %v, got %v", name, c.wantSentinel, err)
			}
		})
	}
}

// Whitespace-only required fields are also rejected — TrimSpace
// guards against accidental "    " env values.
func TestLoadBrokerConfig_RejectsWhitespaceOnlyAudience(t *testing.T) {
	envSet(t, happyEnv())
	t.Setenv("BROKER_AUDIENCE", "   \t  ")
	_, err := composition.LoadBrokerConfig()
	if !errors.Is(err, composition.ErrBrokerConfigMissingAudience) {
		t.Errorf("whitespace audience must be rejected, got %v", err)
	}
}

// CSV with only commas / whitespace produces an empty allowlist
// which is rejected for the required SA lists.
func TestLoadBrokerConfig_RejectsEmptyCSVForRequiredSAs(t *testing.T) {
	for _, raw := range []string{",", " , ,", "\t,\t"} {
		t.Run("gateway-csv="+raw, func(t *testing.T) {
			envSet(t, happyEnv())
			t.Setenv("BROKER_GATEWAY_SERVICE_SAS", raw)
			_, err := composition.LoadBrokerConfig()
			if !errors.Is(err, composition.ErrBrokerConfigMissingGatewayServiceSAs) {
				t.Errorf("empty-CSV gateway SAs must be rejected, got %v", err)
			}
		})
	}
}

// CSV trimming: trailing comma + whitespace must NOT produce a
// phantom empty entry in the allowlist.
func TestLoadBrokerConfig_TrimsCSVEntries(t *testing.T) {
	envSet(t, happyEnv())
	t.Setenv("BROKER_GATEWAY_SERVICE_SAS", "  a@x.example , b@y.example , ")
	cfg, err := composition.LoadBrokerConfig()
	if err != nil {
		t.Fatalf("LoadBrokerConfig: %v", err)
	}
	if len(cfg.GatewayServiceSAs) != 2 {
		t.Fatalf("GatewayServiceSAs len = %d, want 2", len(cfg.GatewayServiceSAs))
	}
	if cfg.GatewayServiceSAs[0] != "a@x.example" || cfg.GatewayServiceSAs[1] != "b@y.example" {
		t.Errorf("GatewayServiceSAs = %v", cfg.GatewayServiceSAs)
	}
}

// Non-numeric / zero / negative GITHUB_APP_ID is rejected with the
// distinct ErrBrokerConfigInvalidGitHubAppID sentinel so the
// startup-failure log explains *why* the value was bad.
func TestLoadBrokerConfig_RejectsInvalidGitHubAppID(t *testing.T) {
	for _, raw := range []string{"not-a-number", "0", "-1", "12345.6"} {
		t.Run("appid="+raw, func(t *testing.T) {
			envSet(t, happyEnv())
			t.Setenv("GITHUB_APP_ID", raw)
			_, err := composition.LoadBrokerConfig()
			if !errors.Is(err, composition.ErrBrokerConfigInvalidGitHubAppID) {
				t.Errorf("appid=%q want ErrBrokerConfigInvalidGitHubAppID, got %v", raw, err)
			}
		})
	}
}

// BROKER_USE_FIRESTORE_REGISTRY accepts "true" / "1" /
// case-insensitive variants; every other value is false.
func TestLoadBrokerConfig_FirestoreFlagParsing(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"true":  true,
		"True":  true,
		"TRUE":  true,
		"1":     true,
		"yes":   false, // not in the accepted set
		"false": false,
		"0":     false,
	}
	for raw, want := range cases {
		t.Run("flag="+raw, func(t *testing.T) {
			envSet(t, happyEnv())
			t.Setenv("BROKER_USE_FIRESTORE_REGISTRY", raw)
			cfg, err := composition.LoadBrokerConfig()
			if err != nil {
				t.Fatalf("LoadBrokerConfig: %v", err)
			}
			if cfg.UseFirestoreRegistry != want {
				t.Errorf("flag=%q got %v, want %v", raw, cfg.UseFirestoreRegistry, want)
			}
		})
	}
}

// Custom GOOGLE_STS_ISSUER + GOOGLE_JWKS_URL override the defaults
// — useful for local dev / staging against an emulator.
func TestLoadBrokerConfig_CustomGoogleEndpointsOverride(t *testing.T) {
	envSet(t, happyEnv())
	t.Setenv("GOOGLE_STS_ISSUER", "https://staging-sts.example.com")
	t.Setenv("GOOGLE_JWKS_URL", "https://staging-sts.example.com/jwks")
	cfg, err := composition.LoadBrokerConfig()
	if err != nil {
		t.Fatalf("LoadBrokerConfig: %v", err)
	}
	if cfg.GoogleSTSIssuer != "https://staging-sts.example.com" {
		t.Errorf("GoogleSTSIssuer = %q", cfg.GoogleSTSIssuer)
	}
	if cfg.GoogleJWKSURL != "https://staging-sts.example.com/jwks" {
		t.Errorf("GoogleJWKSURL = %q", cfg.GoogleJWKSURL)
	}
}
