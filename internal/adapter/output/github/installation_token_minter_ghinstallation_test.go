package github

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"testing"
)

// validRSAKeyPEM generates a fresh 2048-bit RSA key and returns
// it in the PKCS#1 PEM format that ghinstallation/v2 expects.
func validRSAKeyPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})
}

// Happy ctor: valid app ID + valid private key produces a non-nil
// minter. Mint itself is exercised in Phase 3c integration tests
// against a real GitHub App test secret; this test only confirms
// the wiring + ctor failure paths.
func TestNewGhinstallationMinter_HappyCtor(t *testing.T) {
	m, err := NewGhinstallationMinter(12345, validRSAKeyPEM(t), nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if m == nil {
		t.Errorf("ctor returned nil minter without error")
	}
}

// app_id <= 0 is rejected at ctor time.
func TestNewGhinstallationMinter_RejectsInvalidAppID(t *testing.T) {
	for _, appID := range []int64{0, -1, -12345} {
		_, err := NewGhinstallationMinter(appID, validRSAKeyPEM(t), nil)
		if !errors.Is(err, ErrGhinstallationInvalidAppID) {
			t.Errorf("appID=%d: want ErrGhinstallationInvalidAppID, got %v", appID, err)
		}
	}
}

// Empty / nil private key is rejected at ctor time.
func TestNewGhinstallationMinter_RejectsMissingPrivateKey(t *testing.T) {
	for _, key := range [][]byte{nil, {}} {
		_, err := NewGhinstallationMinter(12345, key, nil)
		if !errors.Is(err, ErrGhinstallationMissingPrivateKey) {
			t.Errorf("key=%v: want ErrGhinstallationMissingPrivateKey, got %v", key, err)
		}
	}
}

// Malformed private key (not a valid PEM RSA key) is rejected at
// ctor time so the failure surface is at startup, not on the first
// inbound broker request.
func TestNewGhinstallationMinter_RejectsMalformedPrivateKey(t *testing.T) {
	_, err := NewGhinstallationMinter(12345, []byte("not-a-pem-key"), nil)
	if err == nil {
		t.Errorf("malformed key must error at ctor time")
	}
	if errors.Is(err, ErrGhinstallationMissingPrivateKey) || errors.Is(err, ErrGhinstallationInvalidAppID) {
		t.Errorf("malformed-key error must wrap a parse error, not the simple sentinels")
	}
}

// nil http.Client falls back to http.DefaultClient — production
// callers can pass nil when they do not need a custom transport.
func TestNewGhinstallationMinter_NilClientFallsBackToDefault(t *testing.T) {
	m, err := NewGhinstallationMinter(12345, validRSAKeyPEM(t), nil)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if m.httpClient == nil {
		t.Errorf("ctor must default httpClient when nil is supplied")
	}
}

// Custom http.Client is propagated unchanged so production
// composition can inject an OTel-instrumented transport.
func TestNewGhinstallationMinter_PreservesCustomClient(t *testing.T) {
	custom := &http.Client{}
	m, err := NewGhinstallationMinter(12345, validRSAKeyPEM(t), custom)
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if m.httpClient != custom {
		t.Errorf("ctor must preserve the supplied client identity")
	}
}

// Compile-time assertion: the production minter satisfies the
// unexported tokenMinter interface used by InstallationTokenBroker.
// (Same assertion as the production file — duplicated here so a
// future test-only refactor that moves the assertion still has
// a guard.)
func TestGhinstallationMinter_SatisfiesTokenMinterInterface(t *testing.T) {
	var _ tokenMinter = (*GhinstallationMinter)(nil)
}
