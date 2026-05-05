package slack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// --- test doubles ---

type recordedDispatchUseCase struct {
	mu   sync.Mutex
	reqs []domain.DispatchRequest
	err  error
}

func (r *recordedDispatchUseCase) DispatchAgentTask(_ context.Context, req domain.DispatchRequest, _ port.NotifyTarget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return r.err
}

func (r *recordedDispatchUseCase) calls() []domain.DispatchRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.DispatchRequest, len(r.reqs))
	copy(out, r.reqs)
	return out
}

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

// --- tests ---

func TestCommandHandler_RejectsInvalidSignature(t *testing.T) {
	uc := &recordedDispatchUseCase{}
	h := NewCommandHandler(uc, "secret-correct")

	req := httptest.NewRequest(http.MethodPost, "/slack/command", bytes.NewBufferString("command=%2Fagent"))
	req.Header.Set("X-Slack-Request-Timestamp", "1700000000")
	req.Header.Set("X-Slack-Signature", "v0=invalidsig")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestCommandHandler_ParsesRoleAndText(t *testing.T) {
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	// Allow async dispatch to complete.
	for i := 0; i < 100 && len(uc.calls()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	calls := uc.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 dispatch, got %d", len(calls))
	}
	if calls[0].Role != domain.AgentRolePaintress {
		t.Errorf("Role=%q", calls[0].Role)
	}
	if calls[0].Text != "fix M-42" {
		t.Errorf("Text=%q", calls[0].Text)
	}
	if calls[0].RequesterID != "U0123ABCD" {
		t.Errorf("RequesterID=%q", calls[0].RequesterID)
	}
	if calls[0].IdempotencyKey == "" {
		t.Error("IdempotencyKey should be auto-generated when absent")
	}
	if calls[0].IssuedAt == 0 {
		t.Error("IssuedAt should be set")
	}
}

func TestCommandHandler_RejectsUnknownRole(t *testing.T) {
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	if len(uc.calls()) != 0 {
		t.Errorf("dispatch must not run for unknown role; got %d", len(uc.calls()))
	}
}

func TestCommandHandler_RejectsEmptyText(t *testing.T) {
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	if len(uc.calls()) != 0 {
		t.Errorf("dispatch must not run for empty text; got %d", len(uc.calls()))
	}
}

func TestCommandHandler_RejectsEmptyCommandField(t *testing.T) {
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	if len(uc.calls()) != 0 {
		t.Errorf("dispatch must not run when command is empty; got %d", len(uc.calls()))
	}
}

func TestCommandHandler_RejectsRoleOnlyInput(t *testing.T) {
	// "/agent paintress" with no task description must yield ephemeral usage hint.
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	if len(uc.calls()) != 0 {
		t.Errorf("dispatch must not run for role-only input; got %d", len(uc.calls()))
	}
}

func TestCommandHandler_ReturnsOkEvenWhenUseCaseErrors(t *testing.T) {
	// Slack 3-second rule: /slack/command must always 200 once HMAC+parse pass,
	// because the use case runs in a goroutine and any failure is reported via
	// response_url asynchronously, not the immediate HTTP response.
	uc := &recordedDispatchUseCase{err: errors.New("downstream boom")}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

	body := formBody(url.Values{
		"command":      {"/agent"},
		"text":         {"paintress fix"},
		"user_id":      {"U0123"},
		"response_url": {"https://hooks.slack.com/x"},
	})
	req := signedCommandRequest(t, secret, body)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even on use case error, got %d", rr.Code)
	}
	for i := 0; i < 100 && len(uc.calls()) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if len(uc.calls()) != 1 {
		t.Errorf("dispatch must still be attempted; got %d", len(uc.calls()))
	}
}

func TestCommandHandler_RejectsMalformedFormBody(t *testing.T) {
	// Body that ParseForm cannot parse (invalid percent-encoding).
	uc := &recordedDispatchUseCase{}
	secret := "test-secret"
	h := NewCommandHandler(uc, secret)

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
	if len(uc.calls()) != 0 {
		t.Errorf("dispatch must not run on parse error; got %d", len(uc.calls()))
	}
}

func TestParseSlashCommandText_ExtractsRoleAndRest(t *testing.T) {
	cases := []struct {
		in       string
		wantRole string
		wantText string
	}{
		{"paintress fix M-42", "paintress", "fix M-42"},
		{"sightjack scan ENG project", "sightjack", "scan ENG project"},
		{"amadeus", "amadeus", ""}, // role only — caller may treat as missing text
	}
	for _, tc := range cases {
		role, text := parseSlashCommandText(tc.in)
		if role != tc.wantRole {
			t.Errorf("in=%q role=%q want=%q", tc.in, role, tc.wantRole)
		}
		if text != tc.wantText {
			t.Errorf("in=%q text=%q want=%q", tc.in, text, tc.wantText)
		}
	}
}
