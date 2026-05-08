package composition_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hironow/runops-gateway/internal/composition"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// fakeProjectRegistry is the minimal ProjectRegistry impl required
// to satisfy NewBrokerDependencies — the wiring helper only stores
// the registry pointer; it never invokes registry methods at ctor
// time, so the fake's methods can return canned errors.
type fakeProjectRegistry struct{}

func (fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	return errors.New("fake.Add not used")
}

func (fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return nil, errors.New("fake.List not used")
}

func (fakeProjectRegistry) Get(_ context.Context, _ string) (domain.Project, error) {
	return domain.Project{}, errors.New("fake.Get not used")
}

func (fakeProjectRegistry) Archive(_ context.Context, _ string) error {
	return errors.New("fake.Archive not used")
}

// validKeyPath writes a fresh PEM-encoded RSA key to a temp file
// and returns the path so tests don't need a baked-in fixture.
func validKeyPath(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
	path := filepath.Join(t.TempDir(), "github-app.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// happyConfig produces a fully populated BrokerConfig with a
// freshly generated RSA key on disk so NewBrokerDependencies has
// every dep it needs.
func happyConfig(t *testing.T) *composition.BrokerConfig {
	t.Helper()
	return &composition.BrokerConfig{
		Audience:                "https://broker.example.com",
		GoogleSTSIssuer:         "https://accounts.google.com",
		GoogleJWKSURL:           "https://www.googleapis.com/oauth2/v3/certs",
		OperatorEmails:          []string{"operator@example.com"},
		GatewayServiceSAs:       []string{"gateway-internal@example.iam.gserviceaccount.com"},
		WorkspaceDaemonSAs:      []string{"workspace-daemon@example.iam.gserviceaccount.com"},
		GitHubAppID:             12345,
		GitHubAppPrivateKeyPath: validKeyPath(t),
		UseFirestoreRegistry:    false,
	}
}

// nil BrokerConfig produces a precise sentinel so the cmd/server
// composition root can render a startup-time error.
func TestNewBrokerDependencies_NilConfigRejected(t *testing.T) {
	_, err := composition.NewBrokerDependencies(context.Background(), nil, fakeProjectRegistry{}, nil)
	if !errors.Is(err, composition.ErrBrokerDependenciesNilConfig) {
		t.Errorf("want ErrBrokerDependenciesNilConfig, got %v", err)
	}
}

// nil ProjectRegistry produces a precise sentinel.
func TestNewBrokerDependencies_NilProjectRegistryRejected(t *testing.T) {
	cfg := happyConfig(t)
	_, err := composition.NewBrokerDependencies(context.Background(), cfg, nil, nil)
	if !errors.Is(err, composition.ErrBrokerDependenciesNilProjectRegistry) {
		t.Errorf("want ErrBrokerDependenciesNilProjectRegistry, got %v", err)
	}
}

// Missing private key file produces a wrapped fs error so the
// composition root can distinguish "key file not found" from
// "key bytes invalid".
func TestNewBrokerDependencies_MissingKeyFileSurfaces(t *testing.T) {
	cfg := happyConfig(t)
	cfg.GitHubAppPrivateKeyPath = "/tmp/path/that/does/not/exist/key.pem"
	_, err := composition.NewBrokerDependencies(context.Background(), cfg, fakeProjectRegistry{}, nil)
	if err == nil {
		t.Fatalf("want error for missing key file, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want fs error, got %v", err)
	}
}

// Malformed PEM produces a non-nil error (the ghinstallation ctor
// rejects the bytes). The test asserts the error is non-nil rather
// than a specific sentinel because the wrapped error comes from
// ghinstallation's parser.
func TestNewBrokerDependencies_MalformedKeyRejected(t *testing.T) {
	cfg := happyConfig(t)
	bogusPath := filepath.Join(t.TempDir(), "bogus.pem")
	if err := os.WriteFile(bogusPath, []byte("not-a-real-pem"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg.GitHubAppPrivateKeyPath = bogusPath
	_, err := composition.NewBrokerDependencies(context.Background(), cfg, fakeProjectRegistry{}, nil)
	if err == nil {
		t.Errorf("want error for malformed key, got nil")
	}
}

// Happy path: every dep wires successfully and the returned struct
// has non-nil Service / Authenticator / AgentSessionRegistry. JWKs
// fetch is a real HTTP call to the configured URL — we redirect
// to a tiny stub server to keep the test offline.
func TestNewBrokerDependencies_HappyPathWiresAllDependencies(t *testing.T) {
	cfg := happyConfig(t)
	// Point the JWKs URL at a known-bad host so the JWKs fetch
	// does NOT actually hit Google during the unit test. The
	// fetch failure is acceptable here: keyfunc/v3's
	// NewDefaultCtx returns the verifier even when initial fetch
	// fails (subsequent verifications will retry).
	cfg.GoogleJWKSURL = "http://127.0.0.1:0/jwks"

	deps, err := composition.NewBrokerDependencies(context.Background(), cfg, fakeProjectRegistry{}, nil)
	if err != nil {
		t.Fatalf("NewBrokerDependencies: %v", err)
	}
	if deps.Service == nil {
		t.Errorf("Service is nil")
	}
	if deps.Authenticator == nil {
		t.Errorf("Authenticator is nil")
	}
	if deps.AgentSessionRegistry == nil {
		t.Errorf("AgentSessionRegistry is nil")
	}
}
