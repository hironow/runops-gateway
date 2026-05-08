package secret

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	gax "github.com/googleapis/gax-go/v2"
)

// secretAccessor is the seam between SecretManagerPrivateKeyFetcher
// and the actual *secretmanager.Client. The concrete client struct
// satisfies this interface; tests substitute a fake. The variadic
// gax.CallOption signature mirrors the upstream client API so we
// don't pin it through a wrapper.
type secretAccessor interface {
	AccessSecretVersion(ctx context.Context, req *secretmanagerpb.AccessSecretVersionRequest, opts ...gax.CallOption) (*secretmanagerpb.AccessSecretVersionResponse, error)
}

// SecretManagerPrivateKeyFetcher reads the GitHub App private key
// from Cloud Secret Manager. Used by production deployments where
// the key MUST NOT touch the local filesystem (= avoid mount-time
// snapshot leaks + container image bake-in mistakes).
type SecretManagerPrivateKeyFetcher struct {
	accessor   secretAccessor
	secretName string
}

// secretNameRegex pins the canonical Secret Manager resource name
// shape: projects/<project>/secrets/<secret>/versions/<version>.
// Accepts "latest" as the version literal; numeric versions also
// pass.
var secretNameRegex = regexp.MustCompile(`^projects/[^/]+/secrets/[^/]+/versions/(latest|[0-9]+)$`)

// NewSecretManagerPrivateKeyFetcher builds the production fetcher.
// secretName MUST be a fully-qualified Secret Manager resource
// name; otherwise the ctor returns ErrSecretNameInvalid so the
// composition root surfaces the misconfiguration at startup.
//
// The function constructs a real *secretmanager.Client; callers
// that need a fake (= unit tests) use the package-internal
// newSecretManagerPrivateKeyFetcherWithAccessor.
func NewSecretManagerPrivateKeyFetcher(ctx context.Context, secretName string) (*SecretManagerPrivateKeyFetcher, error) {
	if !secretNameRegex.MatchString(secretName) {
		return nil, ErrSecretNameInvalid
	}
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret_manager: NewClient: %w", err)
	}
	return &SecretManagerPrivateKeyFetcher{accessor: client, secretName: secretName}, nil
}

// newSecretManagerPrivateKeyFetcherWithAccessor is the test-only
// ctor. It bypasses the secretmanager.NewClient bootstrap so
// fakeAccessor implementations can drive the fetcher.
func newSecretManagerPrivateKeyFetcherWithAccessor(accessor secretAccessor, secretName string) (*SecretManagerPrivateKeyFetcher, error) {
	if !secretNameRegex.MatchString(secretName) {
		return nil, ErrSecretNameInvalid
	}
	return &SecretManagerPrivateKeyFetcher{accessor: accessor, secretName: secretName}, nil
}

// Fetch returns the bytes from Secret Manager. The Payload.Data
// is treated as opaque PEM-encoded RSA key material; ghinstallation
// will validate the format on its own. Empty payload is rejected
// at fetch time so a silently-empty secret cannot produce a
// useless minter.
func (f *SecretManagerPrivateKeyFetcher) Fetch(ctx context.Context) ([]byte, error) {
	resp, err := f.accessor.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: f.secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("secret_manager: AccessSecretVersion %q: %w", f.secretName, err)
	}
	if resp == nil || resp.Payload == nil || len(resp.Payload.Data) == 0 {
		return nil, fmt.Errorf("secret_manager: empty payload for %q", f.secretName)
	}
	return resp.Payload.Data, nil
}

// Sentinel errors raised by NewSecretManagerPrivateKeyFetcher.
var (
	ErrSecretNameInvalid = errors.New("secret_manager: secret name must match projects/<p>/secrets/<s>/versions/<v>")
)
