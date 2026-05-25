package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
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

// --- fakes for ProjectRegistry + PendingStore ---

type fakeProjectRegistry struct {
	listResult []domain.Project
}

func (f *fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error { return nil }
func (f *fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return f.listResult, nil
}
func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	return domain.Project{ID: id, Status: domain.ProjectStatusActive}, nil
}
func (f *fakeProjectRegistry) Archive(_ context.Context, _ string) error { return nil }

type fakePendingStore struct{}

func (f *fakePendingStore) CreateIfNotExists(_ context.Context, _ domain.PendingApproval) (domain.PendingApproval, error) {
	return domain.PendingApproval{}, errors.New("not used in test")
}
func (f *fakePendingStore) Get(_ context.Context, _ string) (domain.PendingApproval, error) {
	return domain.PendingApproval{}, port.ErrPendingNotFound
}
func (f *fakePendingStore) Transition(_ context.Context, _ string, _ domain.PendingStatus, _ *time.Time) error {
	return errors.New("not used in test")
}

// validCfg builds a wiring config with all deps populated (= the happy-path baseline).
func validCfg(t *testing.T, registryToken string) rpcWiringConfig {
	t.Helper()
	return rpcWiringConfig{
		flagEnabled:     true,
		registryPath:    writeTempRegistry(t, validRegistryYAML(t, registryToken)),
		projectRegistry: &fakeProjectRegistry{listResult: []domain.Project{}},
		pendingStore:    &fakePendingStore{},
	}
}

// --- existing behaviours (flag off / registry absent / parse failed) ---

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
	rec := newCapturingRecorder()
	r, _ := http.NewRequest(http.MethodPost, "/rpc", nil)
	mux.ServeHTTP(rec, r)
	if rec.code != http.StatusNotFound {
		t.Errorf("expected 404 for unregistered /rpc, got %d", rec.code)
	}
}

func TestWireRPCEndpoint_FlagOn_RegistryAbsent_NotRegistered(t *testing.T) {
	// given - ADR 0040 §identity contract: registry 不在 = endpoint 不在 (= fail-closed)
	cfg := rpcWiringConfig{
		flagEnabled:  true,
		registryPath: "",
	}
	mux := http.NewServeMux()

	// when
	wired, err := wireRPCEndpoint(mux, cfg)

	// then
	if err != nil {
		t.Fatalf("expected no error when registry path absent, got %v", err)
	}
	if wired {
		t.Errorf("expected wired=false when registry path is empty")
	}
}

func TestWireRPCEndpoint_FlagOn_RegistryParseFailed_StartupFatal(t *testing.T) {
	// given - all deps present, only registry parse fails
	path := writeTempRegistry(t, "tokens: invalid-yaml-not-a-list")
	cfg := rpcWiringConfig{
		flagEnabled:     true,
		registryPath:    path,
		projectRegistry: &fakeProjectRegistry{},
		pendingStore:    &fakePendingStore{},
	}
	mux := http.NewServeMux()

	// when
	_, err := wireRPCEndpoint(mux, cfg)

	// then
	if err == nil {
		t.Fatalf("expected fatal error for parse failure")
	}
}

func TestWireRPCEndpoint_FlagOn_AllDepsPresent_Registered(t *testing.T) {
	// given
	cfg := validCfg(t, "alice-secret")
	mux := http.NewServeMux()

	// when
	wired, err := wireRPCEndpoint(mux, cfg)

	// then
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if !wired {
		t.Errorf("expected wired=true with all deps")
	}
	rec := newCapturingRecorder()
	r, _ := http.NewRequest(http.MethodPost, "/rpc", nil)
	mux.ServeHTTP(rec, r)
	if rec.code == http.StatusNotFound {
		t.Errorf("expected /rpc registered, got 404")
	}
}

// --- §B-4 fail-closed deps + end-to-end method dispatch ---

func TestWireRPCEndpoint_ProjectRegistryNil_Errors(t *testing.T) {
	// given
	cfg := rpcWiringConfig{
		flagEnabled:     true,
		registryPath:    writeTempRegistry(t, validRegistryYAML(t, "tok")),
		projectRegistry: nil,
		pendingStore:    &fakePendingStore{},
	}
	mux := http.NewServeMux()

	// when
	_, err := wireRPCEndpoint(mux, cfg)

	// then
	if err == nil {
		t.Fatalf("expected error when projectRegistry is nil while flag on")
	}
}

func TestWireRPCEndpoint_PendingStoreNil_Errors(t *testing.T) {
	// given
	cfg := rpcWiringConfig{
		flagEnabled:     true,
		registryPath:    writeTempRegistry(t, validRegistryYAML(t, "tok")),
		projectRegistry: &fakeProjectRegistry{},
		pendingStore:    nil,
	}
	mux := http.NewServeMux()

	// when
	_, err := wireRPCEndpoint(mux, cfg)

	// then
	if err == nil {
		t.Fatalf("expected error when pendingStore is nil while flag on")
	}
}

func TestWireRPCEndpoint_RegistersReadOnlyMethods(t *testing.T) {
	// given - full wiring + httptest server to round-trip a real JSON-RPC call
	cfg := validCfg(t, "alice-secret")
	mux := http.NewServeMux()
	if _, err := wireRPCEndpoint(mux, cfg); err != nil {
		t.Fatalf("wireRPCEndpoint: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// when - call runops.admin.project.get with valid bearer
	body := strings.NewReader(`{"jsonrpc":"2.0","method":"runops.admin.project.get","params":{"id":"alpha"},"id":1}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer alice-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// then
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTP status: got %d, want 200", resp.StatusCode)
	}
	var env struct {
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(env.Error) > 0 && string(env.Error) != "null" {
		t.Errorf("expected success envelope, got error: %s", env.Error)
	}
	if env.Result["project"] == nil {
		t.Errorf("expected project field in result, got: %+v", env.Result)
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
