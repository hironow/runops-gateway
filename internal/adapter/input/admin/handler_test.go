package admin

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

const testToken = "secret-bearer-token-abcDEF123"

// fakeRegistry is a minimal in-memory port.ProjectRegistry for handler
// unit tests. Only the operations the handler exercises are implemented;
// the rest panic so any future contract drift fails loud.
type fakeRegistry struct {
	mu       sync.Mutex
	projects map[string]domain.Project
	addErr   error
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{projects: map[string]domain.Project{}}
}

func (f *fakeRegistry) Add(_ context.Context, p domain.Project) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	if err := domain.ValidateProjectID(p.ID); err != nil {
		return err
	}
	if _, ok := f.projects[p.ID]; ok {
		return domain.ErrProjectAlreadyExists
	}
	if p.Status == "" {
		p.Status = domain.ProjectStatusActive
	}
	f.projects[p.ID] = p
	return nil
}

func (f *fakeRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeRegistry) List(_ context.Context, filter port.ProjectListFilter) ([]domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Project, 0, len(f.projects))
	for _, p := range f.projects {
		if filter.Status != "" && p.Status != filter.Status {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeRegistry) Archive(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return domain.ErrProjectNotFound
	}
	p.Status = domain.ProjectStatusArchived
	f.projects[id] = p
	return nil
}

// captureSlogBuffer redirects slog default to a buffer so tests can
// assert that secrets do not appear in logs.
func captureSlogBuffer(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestHandler_Authorize_TokenNormalization(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"valid", "Bearer " + testToken, true},
		{"valid bearer lowercase", "bearer " + testToken, true},
		{"valid BEARER uppercase", "BEARER " + testToken, true},

		{"missing header", "", false},
		{"prefix only", "Bearer ", false},
		{"prefix only no space", "Bearer", false},
		{"wrong prefix", "Token " + testToken, false},
		{"two spaces between prefix and token", "Bearer  " + testToken, false},
		{"leading whitespace", " Bearer " + testToken, false},
		{"trailing newline", "Bearer " + testToken + "\n", false},
		{"trailing space", "Bearer " + testToken + " ", false},
		{"token internal whitespace", "Bearer abc def", false},
		{"token mismatch", "Bearer wrong-token", false},
	}
	h := NewHandler(newFakeRegistry(), testToken)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/projects", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			got := h.authorize(req)
			if got != tc.want {
				t.Errorf("authorize(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestHandler_Authorize_NeverLogsToken(t *testing.T) {
	logBuf := captureSlogBuffer(t)
	h := NewHandler(newFakeRegistry(), testToken)

	// Drive the auth path with a wrong token; handler must log a
	// constant message that does NOT include the received header
	// value or any portion of the configured token.
	req := httptest.NewRequest(http.MethodGet, "/admin/projects", nil)
	req.Header.Set("Authorization", "Bearer wrong-something-leaky")
	rr := httptest.NewRecorder()

	mux := http.NewServeMux()
	h.Register(mux)
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	logged := logBuf.String()
	if strings.Contains(logged, testToken) {
		t.Errorf("log output leaked configured token: %q", logged)
	}
	if strings.Contains(logged, "wrong-something-leaky") {
		t.Errorf("log output leaked received bearer value: %q", logged)
	}
	if strings.Contains(rr.Body.String(), testToken) || strings.Contains(rr.Body.String(), "wrong-something-leaky") {
		t.Errorf("response body leaked token material: %q", rr.Body.String())
	}
}

func TestHandler_Add_HappyPath(t *testing.T) {
	reg := newFakeRegistry()
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"id":"foo","github_org":"hironow","github_repo":"demo","workspace_path":"/path/foo","slack_default_channel":"#runops"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("Add: want 201, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "foo") {
		t.Errorf("response should mention foo, got: %s", rr.Body.String())
	}
	if _, ok := reg.projects["foo"]; !ok {
		t.Errorf("project foo should be persisted")
	}
}

func TestHandler_Add_RejectsInvalidID(t *testing.T) {
	reg := newFakeRegistry()
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"id":"bad id","github_org":"hironow","github_repo":"demo","workspace_path":"/p"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid id: want 400, got %d", rr.Code)
	}
}

func TestHandler_Add_RejectsDuplicate(t *testing.T) {
	reg := newFakeRegistry()
	reg.projects["foo"] = domain.Project{ID: "foo", Status: domain.ProjectStatusActive}
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	body := `{"id":"foo","github_org":"h","github_repo":"r","workspace_path":"/w"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate: want 409, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandler_List_FiltersByStatus(t *testing.T) {
	reg := newFakeRegistry()
	reg.projects["foo"] = domain.Project{ID: "foo", Status: domain.ProjectStatusActive}
	reg.projects["bar"] = domain.Project{ID: "bar", Status: domain.ProjectStatusArchived}
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	cases := []struct {
		name       string
		query      string
		wantHasFoo bool
		wantHasBar bool
		wantStatus int
	}{
		{"no filter (all)", "", true, true, http.StatusOK},
		{"status=all", "?status=all", true, true, http.StatusOK},
		{"status=active", "?status=active", true, false, http.StatusOK},
		{"status=archived", "?status=archived", false, true, http.StatusOK},
		{"status=invalid", "?status=garbage", false, false, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/projects"+tc.query, nil)
			req.Header.Set("Authorization", "Bearer "+testToken)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Fatalf("got %d, want %d (body=%s)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			body := rr.Body.String()
			if got := strings.Contains(body, "\"foo\""); got != tc.wantHasFoo {
				t.Errorf("body has foo=%v, want %v (body=%s)", got, tc.wantHasFoo, body)
			}
			if got := strings.Contains(body, "\"bar\""); got != tc.wantHasBar {
				t.Errorf("body has bar=%v, want %v", got, tc.wantHasBar)
			}
		})
	}
}

func TestHandler_Get_NotFound(t *testing.T) {
	reg := newFakeRegistry()
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/projects/ghost", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("not found: want 404, got %d", rr.Code)
	}
}

func TestHandler_Get_ReturnsProject(t *testing.T) {
	reg := newFakeRegistry()
	reg.projects["foo"] = domain.Project{
		ID: "foo", GitHubOrg: "hironow", GitHubRepo: "demo",
		WorkspacePath: "/path/foo", Status: domain.ProjectStatusActive,
	}
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/admin/projects/foo", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "hironow") {
		t.Errorf("body should contain github_org=hironow, got: %s", rr.Body.String())
	}
}

func TestHandler_Archive_ChangesStatus(t *testing.T) {
	reg := newFakeRegistry()
	reg.projects["foo"] = domain.Project{ID: "foo", Status: domain.ProjectStatusActive}
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/projects/foo/archive", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("archive: want 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if reg.projects["foo"].Status != domain.ProjectStatusArchived {
		t.Errorf("status should be archived, got %s", reg.projects["foo"].Status)
	}
}

func TestHandler_Archive_NotFound(t *testing.T) {
	reg := newFakeRegistry()
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/admin/projects/ghost/archive", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("archive missing: want 404, got %d", rr.Code)
	}
}

func TestHandler_AllEndpoints_RejectMissingAuth(t *testing.T) {
	reg := newFakeRegistry()
	h := NewHandler(reg, testToken)
	mux := http.NewServeMux()
	h.Register(mux)

	cases := []struct {
		method string
		url    string
	}{
		{http.MethodPost, "/admin/projects"},
		{http.MethodGet, "/admin/projects"},
		{http.MethodGet, "/admin/projects/foo"},
		{http.MethodPost, "/admin/projects/foo/archive"},
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.url, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.url, nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("want 401, got %d", rr.Code)
			}
		})
	}
}

func TestHandler_String_RedactsToken(t *testing.T) {
	h := NewHandler(newFakeRegistry(), testToken)
	got := h.String()
	if strings.Contains(got, testToken) {
		t.Errorf("String() leaks configured token: %q", got)
	}
	if !strings.Contains(got, "redacted") {
		t.Errorf("String() should signal redaction; got %q", got)
	}
}

// silence unused import errors during partial compile if we ever drop
// errors usage — keep here so go vet recognises the import.
var _ = errors.Is
