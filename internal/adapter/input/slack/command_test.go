package slack

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
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
