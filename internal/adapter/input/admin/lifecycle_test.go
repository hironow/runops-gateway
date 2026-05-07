package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
)

// TestHandler_LifecycleAgainstSQLite drives the full admin endpoint
// stack against a real SQLiteProjectRegistry sitting on a t.TempDir
// state.db. No emulator, no build tag — the existing Test job exercises
// it on every push (#0012, ADR 0030).
func TestHandler_LifecycleAgainstSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "admin-lifecycle.db")
	db, err := state.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	registry := state.NewSQLiteProjectRegistry(db)

	h := NewHandler(registry, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	auth := func(req *http.Request) *http.Request {
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	// 1. Add foo.
	body := `{"id":"foo","github_org":"hironow","github_repo":"demo","workspace_path":"/path/foo"}`
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("Add foo: want 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	// 2. Add bar.
	body = `{"id":"bar","github_org":"hironow","github_repo":"another","workspace_path":"/path/bar"}`
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("Add bar: want 201, got %d", rr.Code)
	}

	// 3. List active: both present.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodGet, "/admin/projects?status=active", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("List active: want 200, got %d", rr.Code)
	}
	for _, want := range []string{`"foo"`, `"bar"`} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("List active should mention %s, got: %s", want, rr.Body.String())
		}
	}

	// 4. Show foo round-trip.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodGet, "/admin/projects/foo", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("Show foo: want 200, got %d", rr.Code)
	}
	for _, want := range []string{"hironow", "demo", "/path/foo", "active"} {
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("Show foo missing %q in %q", want, rr.Body.String())
		}
	}

	// 5. Archive foo + idempotent re-archive.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodPost, "/admin/projects/foo/archive", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("Archive foo: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "archived") {
		t.Errorf("Archive response should reflect status=archived, got: %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodPost, "/admin/projects/foo/archive", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("Archive foo (idempotent): want 200, got %d", rr.Code)
	}

	// 6. List archived: only foo.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodGet, "/admin/projects?status=archived", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("List archived: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"foo"`) {
		t.Errorf("List archived should mention foo: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"bar"`) {
		t.Errorf("List archived should NOT mention bar: %s", rr.Body.String())
	}

	// 7. List active post-archive: only bar.
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, auth(httptest.NewRequest(http.MethodGet, "/admin/projects?status=active", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("List active post-archive: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"bar"`) {
		t.Errorf("List active post-archive should mention bar: %s", rr.Body.String())
	}
}
