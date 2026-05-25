package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// --- helpers ---

func signedCommandRequest(t *testing.T, secret, body string) *http.Request {
	t.Helper()
	// Use the current second so the request stays inside the freshness window
	// enforced by VerifySignature (ADR 0016 / Issue 0019).
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v0:" + ts + ":" + body))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/slack/command", strings.NewReader(body))
	req.Header.Set("X-Slack-Request-Timestamp", ts)
	req.Header.Set("X-Slack-Signature", sig)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func formBody(values url.Values) string {
	return values.Encode()
}

// decodeConfirmationButtons returns the (approveValue, denyValue) embedded in
// the rendered Block Kit response body. Used by tests that assert the Approve
// click would carry the right dispatch payload.
func decodeConfirmationButtons(t *testing.T, body []byte) (approve, deny string) {
	t.Helper()
	var resp struct {
		ResponseType string `json:"response_type"`
		Blocks       []struct {
			Type     string `json:"type"`
			Elements []struct {
				ActionID string `json:"action_id"`
				Value    string `json:"value"`
			} `json:"elements,omitempty"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode confirmation body: %v\n%s", err, body)
	}
	if resp.ResponseType != "ephemeral" {
		t.Errorf("response_type=%q, want ephemeral", resp.ResponseType)
	}
	for _, b := range resp.Blocks {
		for _, el := range b.Elements {
			switch el.ActionID {
			case "dispatch_approve":
				approve = el.Value
			case "dispatch_deny":
				deny = el.Value
			}
		}
	}
	return approve, deny
}

// --- tests ---

func TestCommandHandler_RejectsInvalidSignature(t *testing.T) {
	h := NewCommandHandler("secret-correct")

	req := httptest.NewRequest(http.MethodPost, "/slack/command", bytes.NewBufferString("command=%2Fagent"))
	req.Header.Set("X-Slack-Request-Timestamp", "1700000000")
	req.Header.Set("X-Slack-Signature", "v0=invalidsig")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestCommandHandler_BuildsConfirmationBlockKit(t *testing.T) {
	secret := "test-secret"
	h := NewCommandHandler(secret)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	approve, deny := decodeConfirmationButtons(t, rr.Body.Bytes())
	if approve == "" {
		t.Fatal("dispatch_approve button missing")
	}
	if deny == "" {
		t.Fatal("dispatch_deny button missing")
	}

	// The Approve value must round-trip into a dispatchActionValue with the
	// parsed role / text / requester intact (so InteractiveHandler can
	// reconstruct the DispatchRequest on click).
	dv, err := parseDispatchActionValue(approve)
	if err != nil {
		t.Fatalf("approve value did not round-trip: %v", err)
	}
	if dv.Role != "paintress" || dv.Text != "fix M-42" || dv.RequesterID != "U0123ABCD" {
		t.Errorf("decoded dispatchActionValue=%+v", dv)
	}
	if dv.IdempotencyKey == "" {
		t.Error("IdempotencyKey should be auto-generated and embedded")
	}
	if dv.IssuedAt == 0 {
		t.Error("IssuedAt should be set")
	}
}

func TestCommandHandler_RejectsUnknownRole(t *testing.T) {
	secret := "test-secret"
	h := NewCommandHandler(secret)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"phonewave do something"}, // phonewave is courier, not a target
		"user_id":      {"U0123"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with ephemeral error, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ephemeral") {
		t.Errorf("expected ephemeral response, got: %s", rr.Body.String())
	}
	approve, deny := decodeConfirmationButtons(t, rr.Body.Bytes())
	if approve != "" || deny != "" {
		t.Errorf("dispatch confirmation must not appear for unknown role; got approve=%q deny=%q", approve, deny)
	}
}

func TestCommandHandler_RejectsEmptyText(t *testing.T) {
	secret := "test-secret"
	h := NewCommandHandler(secret)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {""},
		"user_id":      {"U0123"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with ephemeral error, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ephemeral") {
		t.Errorf("expected ephemeral response, got: %s", rr.Body.String())
	}
}

func TestCommandHandler_RejectsEmptyCommandField(t *testing.T) {
	secret := "test-secret"
	h := NewCommandHandler(secret)

	body := formBody(url.Values{
		// command field intentionally omitted
		"text":         {"paintress fix"},
		"user_id":      {"U0123"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with ephemeral error, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "command") {
		t.Errorf("expected error mentioning command field, got: %s", rr.Body.String())
	}
}

func TestCommandHandler_RejectsRoleOnlyInput(t *testing.T) {
	// "/agent paintress" with no task description must yield ephemeral usage hint.
	secret := "test-secret"
	h := NewCommandHandler(secret)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress"}, // role only, no free text
		"user_id":      {"U0123"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "/agent") {
		t.Errorf("expected usage hint mentioning /agent, got: %s", rr.Body.String())
	}
}

func TestCommandHandler_RejectsMalformedFormBody(t *testing.T) {
	// Body that ParseForm cannot parse (invalid percent-encoding).
	secret := "test-secret"
	h := NewCommandHandler(secret)

	malformedBody := "command=%2Fagent&text=%ZZ" // %ZZ is not valid percent-encoding
	req := signedCommandRequest(t, secret, malformedBody)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with ephemeral, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "リクエスト形式") {
		t.Errorf("expected ephemeral parse error, got: %s", rr.Body.String())
	}
}

func TestParseSlashCommandText_ExtractsRoleAndRest(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantRole    string
		wantProject string
		wantText    string
		wantErr     bool
	}{
		// Backward-compatible cases (no --project flag).
		{"role + text", "paintress fix M-42", "paintress", "", "fix M-42", false},
		{"role + multi-token text", "sightjack scan ENG project", "sightjack", "", "scan ENG project", false},
		{"role only", "amadeus", "amadeus", "", "", false},

		// --project= form.
		{"role + --project= + text", "paintress --project=foo fix it", "paintress", "foo", "fix it", false},

		// --project (space) form.
		{"role + --project space + text", "paintress --project foo fix it", "paintress", "foo", "fix it", false},

		// Reject cases.
		{"--project= empty value", "paintress --project= fix it", "", "", "", true},
		{"--project space without value", "paintress --project", "", "", "", true},
		{"--project= without text", "paintress --project=foo", "", "", "", true},
		{"--project space without text", "paintress --project foo", "", "", "", true},
		{"duplicate --project=", "paintress --project=foo --project=bar fix it", "", "", "", true},
		{"duplicate --project space", "paintress --project foo --project bar fix it", "", "", "", true},
		{"--project= invalid id (space char rejected by regex via tokenization)", "paintress --project=foo bar fix", "paintress", "foo", "bar fix", false}, // value=foo, text="bar fix" — regex passes
		{"--project= invalid id (regex fail)", "paintress --project=bad@id fix it", "", "", "", true},

		// text-internal --project must NOT be consumed.
		{"text contains --project", "paintress fix --project=ghost it", "paintress", "", "fix --project=ghost it", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, project, text, err := parseSlashCommandText(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if role != tc.wantRole {
				t.Errorf("role=%q want=%q", role, tc.wantRole)
			}
			if project != tc.wantProject {
				t.Errorf("project=%q want=%q", project, tc.wantProject)
			}
			if text != tc.wantText {
				t.Errorf("text=%q want=%q", text, tc.wantText)
			}
		})
	}
}

// fakeProjectRegistry is an in-memory port.ProjectRegistry stub used by
// the /agent --project=<id> tests. It mirrors only the methods the
// CommandHandler exercises (Get, List); Add/Archive panic so the suite
// fails loud if a future change accidentally writes through validation.
type fakeProjectRegistry struct {
	mu       sync.Mutex
	projects map[string]domain.Project
}

func newFakeRegistry() *fakeProjectRegistry {
	return &fakeProjectRegistry{projects: map[string]domain.Project{}}
}

func (f *fakeProjectRegistry) seed(id string, status domain.ProjectStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[id] = domain.Project{ID: id, Status: status}
}

func (f *fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	panic("fakeProjectRegistry.Add should not be called from CommandHandler tests")
}

func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.projects[id]
	if !ok {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	return p, nil
}

func (f *fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	panic("fakeProjectRegistry.List should not be called from CommandHandler tests")
}

func (f *fakeProjectRegistry) Archive(_ context.Context, _ string) error {
	panic("fakeProjectRegistry.Archive should not be called from CommandHandler tests")
}

// TestCommandHandler_ProjectFlag_AcceptsRegisteredActiveProject covers the
// happy path: --project=foo is registered and active, so the confirmation
// renders and the button payload carries project_id.
func TestCommandHandler_ProjectFlag_AcceptsRegisteredActiveProject(t *testing.T) {
	secret := "test-secret"
	reg := newFakeRegistry()
	reg.seed("foo", domain.ProjectStatusActive)
	h := NewCommandHandler(secret).WithProjectRegistry(reg)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress --project=foo fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Project") {
		t.Errorf("expected confirmation body to render Project line, got: %s", rr.Body.String())
	}
	approve, _ := decodeConfirmationButtons(t, rr.Body.Bytes())
	if approve == "" {
		t.Fatalf("approve button missing")
	}
	dv, err := parseDispatchActionValue(approve)
	if err != nil {
		t.Fatalf("parse approve value: %v", err)
	}
	if dv.ProjectID != "foo" {
		t.Errorf("approve dispatchActionValue.project_id = %q, want %q", dv.ProjectID, "foo")
	}
}

// TestCommandHandler_ProjectFlag_RejectsUnknownProject covers the unknown
// project case: registry.Get returns ErrProjectNotFound, so the handler
// returns an ephemeral error instead of building a confirmation.
func TestCommandHandler_ProjectFlag_RejectsUnknownProject(t *testing.T) {
	secret := "test-secret"
	reg := newFakeRegistry()
	h := NewCommandHandler(secret).WithProjectRegistry(reg)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress --project=ghost fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 ephemeral, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ghost") || !strings.Contains(rr.Body.String(), "not registered") {
		t.Errorf("expected ephemeral 'project not registered' for ghost, got: %s", rr.Body.String())
	}
}

// TestCommandHandler_ProjectFlag_BackwardCompatNoFlag covers the existing
// flow: --project unspecified means project_id is empty in the button
// value, no registry call, no Project line in the confirmation.
func TestCommandHandler_ProjectFlag_BackwardCompatNoFlag(t *testing.T) {
	secret := "test-secret"
	reg := newFakeRegistry() // empty; would 404 if hit
	h := NewCommandHandler(secret).WithProjectRegistry(reg)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	approve, _ := decodeConfirmationButtons(t, rr.Body.Bytes())
	if approve == "" {
		t.Fatalf("approve button missing")
	}
	dv, err := parseDispatchActionValue(approve)
	if err != nil {
		t.Fatalf("parse approve value: %v", err)
	}
	if dv.ProjectID != "" {
		t.Errorf("approve dispatchActionValue.project_id = %q, want empty", dv.ProjectID)
	}
	if strings.Contains(rr.Body.String(), "*Project:*") {
		t.Errorf("Project line should be omitted when --project is not given")
	}
}

// TestCommandHandler_ProjectFlag_RejectsDuplicateFlag covers parser-level
// safety: --project specified twice → ephemeral error, no dispatch.
func TestCommandHandler_ProjectFlag_RejectsDuplicateFlag(t *testing.T) {
	secret := "test-secret"
	reg := newFakeRegistry()
	reg.seed("foo", domain.ProjectStatusActive)
	reg.seed("bar", domain.ProjectStatusActive)
	h := NewCommandHandler(secret).WithProjectRegistry(reg)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress --project=foo --project=bar fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 ephemeral, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "multiple --project") {
		t.Errorf("expected ephemeral 'multiple --project' error, got: %s", rr.Body.String())
	}
}

// TestCommandHandler_ProjectFlag_RejectsArchivedProject covers status-based
// safety: registry.Get returns the row but Status=archived, so the handler
// rejects rather than dispatching to a deprecated workspace.
func TestCommandHandler_ProjectFlag_RejectsArchivedProject(t *testing.T) {
	secret := "test-secret"
	reg := newFakeRegistry()
	reg.seed("retired", domain.ProjectStatusArchived)
	h := NewCommandHandler(secret).WithProjectRegistry(reg)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress --project=retired fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 ephemeral, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "archived") {
		t.Errorf("expected ephemeral 'archived' error for retired project, got: %s", rr.Body.String())
	}
}

// TestCommandHandler_ProjectFlag_RejectsWhenRegistryDisabled covers the
// fail-closed behaviour: when the deployment did not opt in to a registry
// (handler.WithProjectRegistry was never called), --project must be
// rejected loudly so an operator does not silently route to nowhere.
func TestCommandHandler_ProjectFlag_RejectsWhenRegistryDisabled(t *testing.T) {
	secret := "test-secret"
	h := NewCommandHandler(secret) // no .WithProjectRegistry

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress --project=foo fix M-42"},
		"user_id":      {"U0123ABCD"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 ephemeral, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "無効化") {
		t.Errorf("expected ephemeral 'registry disabled' error, got: %s", rr.Body.String())
	}
}
