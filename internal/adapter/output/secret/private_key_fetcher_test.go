package secret_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/secret"
)

// Empty path is rejected at ctor time so the composition root
// surfaces the misconfiguration at startup rather than on the
// first inbound broker request.
func TestNewFilePrivateKeyFetcher_RejectsEmptyPath(t *testing.T) {
	_, err := secret.NewFilePrivateKeyFetcher("")
	if !errors.Is(err, secret.ErrFilePathEmpty) {
		t.Errorf("want ErrFilePathEmpty, got %v", err)
	}
}

// Happy path: file exists with non-empty content → Fetch returns
// the bytes unchanged.
func TestFilePrivateKeyFetcher_Fetch_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	want := []byte("-----BEGIN RSA PRIVATE KEY-----\nplaceholder-bytes\n-----END RSA PRIVATE KEY-----\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, err := secret.NewFilePrivateKeyFetcher(path)
	if err != nil {
		t.Fatalf("NewFilePrivateKeyFetcher: %v", err)
	}
	got, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Missing file: errors.Is os.ErrNotExist surfaces so the
// composition root can render \"key file not found\" precisely.
func TestFilePrivateKeyFetcher_Fetch_MissingFile(t *testing.T) {
	f, err := secret.NewFilePrivateKeyFetcher("/tmp/does/not/exist/key.pem")
	if err != nil {
		t.Fatalf("NewFilePrivateKeyFetcher: %v", err)
	}
	_, err = f.Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch missing file: want error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want fs error, got %v", err)
	}
}

// Empty file is treated as a misconfiguration — an empty PEM
// would silently produce a useless minter, so we surface it as
// an error at fetch time.
func TestFilePrivateKeyFetcher_Fetch_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	f, _ := secret.NewFilePrivateKeyFetcher(path)
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Errorf("empty file must be rejected, got nil")
	}
}
