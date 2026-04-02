package slack

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

type mockUseCase struct {
	approveCh chan domain.ApprovalRequest
	denyCh    chan domain.ApprovalRequest
}

func (m *mockUseCase) ApproveAction(_ context.Context, req domain.ApprovalRequest) error {
	m.approveCh <- req
	return nil
}

func (m *mockUseCase) DenyAction(_ context.Context, req domain.ApprovalRequest) error {
	m.denyCh <- req
	return nil
}

func buildValidRequest(t *testing.T, secret, payloadJSON string) *http.Request {
	t.Helper()
	body := "payload=" + url.QueryEscape(payloadJSON)
	bodyBytes := []byte(body)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	basestring := "v0:" + timestamp + ":" + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(basestring))
	sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Slack-Request-Timestamp", timestamp)
	req.Header.Set("X-Slack-Signature", sig)
	return req
}

func newMockUseCase() *mockUseCase {
	return &mockUseCase{
		approveCh: make(chan domain.ApprovalRequest, 1),
		denyCh:    make(chan domain.ApprovalRequest, 1),
	}
}

func TestHandler_InvalidSignature(t *testing.T) {
	// given
	mock := newMockUseCase()
	handler := NewHandler(mock, "correct-secret")

	body := []byte("payload=test")
	req := httptest.NewRequest(http.MethodPost, "/slack/interactive", bytes.NewBuffer(body))
	req.Header.Set("X-Slack-Request-Timestamp", "1234567890")
	req.Header.Set("X-Slack-Signature", "v0=invalidsig")

	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandler_ValidApprove(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	av := actionValue{
		ResourceType:  "service",
		ResourceNames: "frontend",
		Targets:       "v2",
		Action:        "canary_10",
		IssuedAt:      time.Now().Unix(),
	}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.ResponseURL = "https://hooks.slack.com/response"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "approve", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case req := <-mock.approveCh:
		if req.ApproverID != "U123" {
			t.Errorf("expected approver U123, got %s", req.ApproverID)
		}
		if req.ResourceNames != "frontend" {
			t.Errorf("expected resource frontend, got %s", req.ResourceNames)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ApproveAction")
	}
}

func TestHandler_ValidDeny(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	av := actionValue{
		ResourceType:  "service",
		ResourceNames: "backend",
		Targets:       "v1",
		Action:        "canary_50",
		IssuedAt:      time.Now().Unix(),
	}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U456"
	payload.ResponseURL = "https://hooks.slack.com/response"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "deny", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case req := <-mock.denyCh:
		if req.ApproverID != "U456" {
			t.Errorf("expected approver U456, got %s", req.ApproverID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for DenyAction")
	}
}

func TestHandler_EmptyActions(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	payload := interactivePayload{}
	payload.User.ID = "U789"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_UnknownActionID(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	av := actionValue{ResourceType: "service", ResourceNames: "svc", IssuedAt: time.Now().Unix()}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U999"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "unknown_action", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandler_MalformedActionValue(t *testing.T) {
	// given — action_id is valid but Value is not parseable JSON
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "approve", Value: "not-valid-json"},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then — must return 200 (Slack would retry on non-2xx)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	// ApproveAction must NOT have been called
	select {
	case <-mock.approveCh:
		t.Error("expected ApproveAction NOT to be called on malformed action value")
	default:
	}
}

func TestHandler_MultipleActions_OnlyFirstProcessed(t *testing.T) {
	// given — two actions in the payload; only the first must be dispatched
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	av := actionValue{ResourceType: "service", ResourceNames: "svc", IssuedAt: time.Now().Unix()}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "approve", Value: string(avBytes)},
		{ActionID: "deny", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then — 200 OK, ApproveAction called, DenyAction NOT called
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case <-mock.approveCh:
		// expected
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ApproveAction")
	}
	select {
	case <-mock.denyCh:
		t.Error("expected DenyAction NOT to be called (only first action processed)")
	default:
	}
}

func gzipBase64(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return "gz:" + base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

func TestParseActionValue_PlainJSON_ParsedCorrectly(t *testing.T) {
	// given
	av := actionValue{
		ResourceType:  "service",
		ResourceNames: "frontend,backend",
		Targets:       "rev-001,rev-002",
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}
	b, _ := json.Marshal(av)

	// when
	got, err := parseActionValue(string(b))

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ResourceNames != "frontend,backend" {
		t.Errorf("ResourceNames: got %q, want %q", got.ResourceNames, "frontend,backend")
	}
	if got.Action != "canary_10" {
		t.Errorf("Action: got %q, want %q", got.Action, "canary_10")
	}
}

func TestParseActionValue_GzPrefixed_DecompressedCorrectly(t *testing.T) {
	// given — manually compress a known action value
	original := actionValue{
		ResourceType:  "service",
		ResourceNames: "frontend,backend",
		Targets:       "rev-001,rev-002",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	b, _ := json.Marshal(original)
	compressed := gzipBase64(t, string(b))

	// when
	got, err := parseActionValue(compressed)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ResourceNames != "frontend,backend" {
		t.Errorf("ResourceNames: got %q, want %q", got.ResourceNames, "frontend,backend")
	}
	if got.Action != "canary_30" {
		t.Errorf("Action: got %q, want %q", got.Action, "canary_30")
	}
}

func TestHandler_CompressedButtonValue_Dispatched(t *testing.T) {
	// given — button value is gz: compressed (simulates large multi-service bundle)
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	av := actionValue{
		ResourceType:  "service",
		ResourceNames: "frontend,backend",
		Targets:       "rev-001,rev-002",
		Action:        "canary_10",
		IssuedAt:      time.Now().Unix(),
	}
	b, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.ResponseURL = "https://hooks.slack.com/response"
	payload.Actions = []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	}{
		{ActionID: "approve", Value: gzipBase64(t, string(b))},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case got := <-mock.approveCh:
		if got.ResourceNames != "frontend,backend" {
			t.Errorf("ResourceNames: got %q, want %q", got.ResourceNames, "frontend,backend")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ApproveAction")
	}
}

func TestParseActionValue_LegacySingularFields_FallbackToResourceNames(t *testing.T) {
	// given — legacy payload uses singular field names (resource_name, target, etc.)
	legacy := `{"resource_type":"service","resource_name":"frontend","target":"rev-001","action":"canary_10","issued_at":1700000000}`

	// when
	got, err := parseActionValue(legacy)

	// then — singular fields must be accessible via ResourceName/Target
	// and the handler maps them via firstNonEmpty(ResourceNames, ResourceName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ResourceName (singular) is populated; ResourceNames (plural) is empty
	if got.ResourceName != "frontend" {
		t.Errorf("ResourceName: got %q, want %q", got.ResourceName, "frontend")
	}
	if got.Target != "rev-001" {
		t.Errorf("Target: got %q, want %q", got.Target, "rev-001")
	}
	// Plural fields are empty — handler uses firstNonEmpty(ResourceNames, ResourceName)
	if got.ResourceNames != "" {
		t.Errorf("ResourceNames should be empty for legacy payload, got %q", got.ResourceNames)
	}
}

func TestParseActionValue_InvalidBase64_ReturnsError(t *testing.T) {
	// given — gz: prefix but the base64 part is invalid
	invalid := "gz:!!!not-base64!!!"

	// when
	_, err := parseActionValue(invalid)

	// then
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

func TestParseActionValue_CorruptGzip_ReturnsError(t *testing.T) {
	// given — gz: prefix with valid base64 but the decoded bytes are not a valid gzip stream
	notGzip := base64.RawURLEncoding.EncodeToString([]byte("this is not gzip data"))
	corrupt := "gz:" + notGzip

	// when
	_, err := parseActionValue(corrupt)

	// then
	if err == nil {
		t.Fatal("expected error for corrupt gzip, got nil")
	}
}

func TestHandler_MalformedPayloadJSON(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewHandler(mock, secret)

	req := buildValidRequest(t, secret, `{not valid json}`)
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}
