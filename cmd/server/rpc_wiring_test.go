package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeTempRegistry(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "admin-tokens.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func validRegistryYAML(t *testing.T, token string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	return "tokens:\n" +
		"  - operator_id: U01234ABCD\n" +
		"    token_hash: " + hex.EncodeToString(sum[:]) + "\n" +
		"    email: alice@example.com\n"
}

func TestWireRPCEndpoint_FlagOff_NotRegistered(t *testing.T) {
	// given
	cfg := rpcWiringConfig{
		flagEnabled:  false,
		registryPath: "/nonexistent/whatever.yaml",
	}
	mux := http.NewServeMux()

	// when
	wired, err := wireRPCEndpoint(mux, cfg)

	// then
	if err != nil {
		t.Fatalf("expected no error when flag is off, got %v", err)
	}
	if wired {
		t.Errorf("expected wired=false when flag is off")
	}
	// Verify the route is not handled (= ServeMux returns NotFound for /rpc)
	rec := newCapturingRecorder()
	r, _ := http.NewRequest(http.MethodPost, "/rpc", nil)
	mux.ServeHTTP(rec, r)
	if rec.code != http.StatusNotFound {
		t.Errorf("expected 404 for unregistered /rpc, got %d", rec.code)
	}
}

func TestWireRPCEndpoint_FlagOn_RegistryAbsent_NotRegistered(t *testing.T) {
	// given
	cfg := rpcWiringConfig{
		flagEnabled:  true,
		registryPath: "", // not set
	}
	mux := http.NewServeMux()

	// when
	wired, err := wireRPCEndpoint(mux, cfg)

	// then - §B-3 暫定挙動: registry path 未設定で /rpc 不在
	// (§B-4 で legacy ADMIN_TOKEN fallback 追加時に再評価)
	if err != nil {
		t.Fatalf("expected no error when registry path absent, got %v", err)
	}
	if wired {
		t.Errorf("expected wired=false when registry path is empty")
	}
}

func TestWireRPCEndpoint_FlagOn_RegistryParseFailed_StartupFatal(t *testing.T) {
	// given
	path := writeTempRegistry(t, "tokens: invalid-yaml-not-a-list")
	cfg := rpcWiringConfig{
		flagEnabled:  true,
		registryPath: path,
	}
	mux := http.NewServeMux()

	// when
	_, err := wireRPCEndpoint(mux, cfg)

	// then - parse 失敗は startup-fatal (= caller が log.Fatal する想定)
	if err == nil {
		t.Fatalf("expected fatal error for parse failure")
	}
}

func TestWireRPCEndpoint_FlagOn_RegistryPresent_Registered(t *testing.T) {
	// given
	path := writeTempRegistry(t, validRegistryYAML(t, "alice-secret"))
	cfg := rpcWiringConfig{
		flagEnabled:  true,
		registryPath: path,
	}
	mux := http.NewServeMux()

	// when
	wired, err := wireRPCEndpoint(mux, cfg)

	// then
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !wired {
		t.Errorf("expected wired=true with valid registry")
	}
	// /rpc must NOT be 404 anymore (= it returns 401 for missing auth)
	rec := newCapturingRecorder()
	r, _ := http.NewRequest(http.MethodPost, "/rpc", nil)
	mux.ServeHTTP(rec, r)
	if rec.code == http.StatusNotFound {
		t.Errorf("expected /rpc registered, got 404")
	}
}

// --- minimal http.ResponseWriter for assertions ---

type capturingRecorder struct {
	code   int
	header http.Header
}

func newCapturingRecorder() *capturingRecorder {
	return &capturingRecorder{header: http.Header{}}
}

func (c *capturingRecorder) Header() http.Header         { return c.header }
func (c *capturingRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c *capturingRecorder) WriteHeader(code int)        { c.code = code }
