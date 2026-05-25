// Package secret holds the GitHub App private-key fetcher port and
// adapters (refs#0007 plan v8 §6 step 14 / Phase 2b-2-2). The
// composition root invokes the fetcher once at startup and hands
// the resulting PEM bytes to the ghinstallation minter ctor.
//
// Phase 2b-2-2a (this file) ships the port + the file-system adapter
// that the existing dev / staging deployments already depend on.
// Phase 2b-2-2b will add a Secret Manager-backed adapter that
// production deployments switch to via the Phase 3b-3a env-var
// schema (GITHUB_APP_PRIVATE_KEY_SECRET_NAME — to be added).
package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// PrivateKeyFetcher is the secondary port for sourcing the GitHub
// App private key. The composition root chooses an implementation
// based on which env var is set:
//
//   - GITHUB_APP_PRIVATE_KEY_PATH      → FilePrivateKeyFetcher
//   - GITHUB_APP_PRIVATE_KEY_SECRET_NAME → Secret Manager (Phase 2b-2-2b)
//
// The port is single-method and synchronous because the call only
// happens once per process at startup; there is no expectation of
// caching, retry, or rotation at this layer.
type PrivateKeyFetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// FilePrivateKeyFetcher reads the PEM-encoded private key from a
// path on the local filesystem. Used by dev / staging deployments
// where the key is mounted as a volume.
type FilePrivateKeyFetcher struct {
	path string
}

// NewFilePrivateKeyFetcher returns a fetcher reading from path.
// Returns ErrFilePathEmpty if path is the empty string after
// trimming, so the composition root surfaces the misconfiguration
// at construction rather than at first fetch.
func NewFilePrivateKeyFetcher(path string) (*FilePrivateKeyFetcher, error) {
	if path == "" {
		return nil, ErrFilePathEmpty
	}
	return &FilePrivateKeyFetcher{path: path}, nil
}

// Fetch reads the PEM bytes from disk. The os.ReadFile error
// surfaces unchanged so the composition root can render the right
// startup-failure message (errors.Is os.ErrNotExist for the
// "wrong path" case).
func (f *FilePrivateKeyFetcher) Fetch(_ context.Context) ([]byte, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("secret: read private key file %q: %w", f.path, err)
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("secret: private key file %q is empty", f.path)
	}
	return b, nil
}

// Sentinel errors raised by NewFilePrivateKeyFetcher.
var (
	ErrFilePathEmpty = errors.New("secret: file path must be non-empty")
)
