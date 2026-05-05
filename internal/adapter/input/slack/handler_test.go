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
	"sync"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

type mockUseCase struct {
	approveCh chan domain.ApprovalRequest
	denyCh    chan domain.ApprovalRequest
}

func (m *mockUseCase) ApproveAction(_ context.Context, req domain.ApprovalRequest, _ port.NotifyTarget) error {
	m.approveCh <- req
	return nil
}

func (m *mockUseCase) DenyAction(_ context.Context, req domain.ApprovalRequest, _ port.NotifyTarget) error {
	m.denyCh <- req
	return nil
}

// stubNotifier is a no-op notifier for handler tests (timeout fallback path).
type stubNotifier struct{}

func (s *stubNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return nil
}
func (s *stubNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return nil
}
func (s *stubNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _, _ string) error {
	return nil
}
func (s *stubNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, _, _ *domain.ApprovalRequest) error {
	return nil
}
func (s *stubNotifier) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, _ string, _, _, _ *domain.ApprovalRequest) error {
	return nil
}

var testNotifier = &stubNotifier{}

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

func TestInteractiveHandler_InvalidSignature(t *testing.T) {
	// given
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, "correct-secret")

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

func TestInteractiveHandler_ValidApprove(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{
		Project:       "test-project",
		Location:      "asia-northeast1",
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
	payload.Actions = []interactiveAction{
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
		if req.Project != "test-project" {
			t.Errorf("expected project test-project, got %s", req.Project)
		}
		if req.Location != "asia-northeast1" {
			t.Errorf("expected location asia-northeast1, got %s", req.Location)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ApproveAction")
	}
}

func TestInteractiveHandler_ValidDeny(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{
		Project:       "test-project",
		Location:      "asia-northeast1",
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
	payload.Actions = []interactiveAction{
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

func TestInteractiveHandler_EmptyActions(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	payload := interactivePayload{}
	payload.User.ID = "U789"
	payload.Actions = []interactiveAction{}
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

func TestInteractiveHandler_UnknownActionID(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{Project: "test-project", Location: "asia-northeast1", ResourceType: "service", ResourceNames: "svc", IssuedAt: time.Now().Unix()}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U999"
	payload.Actions = []interactiveAction{
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

// --- Phase 1 / F-5 dispatch_* routing tests (DispatchUseCase wiring) ---

type recordedDispatchUseCase struct {
	mu    sync.Mutex
	calls []domain.DispatchRequest
}

func (r *recordedDispatchUseCase) DispatchAgentTask(_ context.Context, req domain.DispatchRequest, _ port.NotifyTarget) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req)
	return nil
}

func (r *recordedDispatchUseCase) snapshot() []domain.DispatchRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.DispatchRequest, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestInteractiveHandler_DispatchApprove_CallsDispatchUseCase(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	disp := &recordedDispatchUseCase{}
	handler := NewInteractiveHandler(mock, disp, testNotifier, secret)

	// Approve button payload — what CommandHandler embeds in dispatch_approve.
	dv := dispatchActionValue{
		Role:           "paintress",
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD",
		IdempotencyKey: "k-001",
		IssuedAt:       time.Now().Unix(),
	}
	dvBytes, _ := json.Marshal(dv)

	payload := interactivePayload{}
	payload.User.ID = "U0123ABCD"
	payload.ResponseURL = "https://hooks.slack.com/x"
	payload.Actions = []interactiveAction{
		{ActionID: "dispatch_approve", Value: string(dvBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(disp.snapshot()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	calls := disp.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 DispatchAgentTask call, got %d", len(calls))
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
	if calls[0].IdempotencyKey != "k-001" {
		t.Errorf("IdempotencyKey=%q", calls[0].IdempotencyKey)
	}
	// And ApproveAction / DenyAction must NOT have been called.
	select {
	case <-mock.approveCh:
		t.Error("ApproveAction must not be called for dispatch_approve")
	case <-mock.denyCh:
		t.Error("DenyAction must not be called for dispatch_approve")
	default:
	}
}

func TestInteractiveHandler_DispatchApprove_RejectsImpersonation(t *testing.T) {
	// given — clicker (U_other) is NOT the original requester (U0123ABCD)
	secret := "test-secret"
	mock := newMockUseCase()
	disp := &recordedDispatchUseCase{}
	handler := NewInteractiveHandler(mock, disp, testNotifier, secret)

	dv := dispatchActionValue{
		Role:           "paintress",
		Text:           "fix M-42",
		RequesterID:    "U0123ABCD", // payload-bound original requester
		IdempotencyKey: "k-impersonate",
		IssuedAt:       time.Now().Unix(),
	}
	dvBytes, _ := json.Marshal(dv)

	payload := interactivePayload{}
	payload.User.ID = "U_other" // clicker is someone else
	payload.ResponseURL = "https://hooks.slack.com/x"
	payload.Actions = []interactiveAction{
		{ActionID: "dispatch_approve", Value: string(dvBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// give the goroutine a chance to NOT run
	time.Sleep(50 * time.Millisecond)
	if got := disp.snapshot(); len(got) != 0 {
		t.Errorf("DispatchAgentTask must not be called when clicker != requester; got %d calls (%+v)", len(got), got)
	}
}

func TestInteractiveHandler_DispatchDeny_RejectsImpersonation(t *testing.T) {
	// given — Deny is also restricted to the original requester to prevent
	// griefers from dismissing other people's pending confirmations.
	secret := "test-secret"
	mock := newMockUseCase()
	disp := &recordedDispatchUseCase{}
	handler := NewInteractiveHandler(mock, disp, testNotifier, secret)

	dv := dispatchActionValue{
		Role:        "paintress",
		Text:        "fix M-42",
		RequesterID: "U0123ABCD",
	}
	dvBytes, _ := json.Marshal(dv)

	payload := interactivePayload{}
	payload.User.ID = "U_other" // clicker is someone else
	payload.ResponseURL = "https://hooks.slack.com/x"
	payload.Actions = []interactiveAction{
		{ActionID: "dispatch_deny", Value: string(dvBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// no use case is invoked either way; this test mainly asserts no panic
	// and silent rejection. The full verification of "impersonation rejected"
	// lives in the approve test above.
	time.Sleep(50 * time.Millisecond)
	if got := disp.snapshot(); len(got) != 0 {
		t.Errorf("DispatchAgentTask must not be called via dispatch_deny; got %d calls", len(got))
	}
}

func TestInteractiveHandler_DispatchDeny_DoesNotInvokeDispatchUseCase(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	disp := &recordedDispatchUseCase{}
	handler := NewInteractiveHandler(mock, disp, testNotifier, secret)

	dv := dispatchActionValue{
		Role:        "paintress",
		Text:        "fix M-42",
		RequesterID: "U0123ABCD",
	}
	dvBytes, _ := json.Marshal(dv)

	payload := interactivePayload{}
	payload.User.ID = "U0123ABCD"
	payload.ResponseURL = "https://hooks.slack.com/x"
	payload.Actions = []interactiveAction{
		{ActionID: "dispatch_deny", Value: string(dvBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// give the goroutine a chance to NOT run
	time.Sleep(50 * time.Millisecond)
	if len(disp.snapshot()) != 0 {
		t.Errorf("DispatchAgentTask must not be called for dispatch_deny; got %d calls", len(disp.snapshot()))
	}
}

// TestInteractiveHandler_DispatchActionIDsDoNotRouteToApprove is a regression
// guard for Phase 0 ChatOps. Phase 1 introduced dispatch_approve / dispatch_deny
// action_ids on the same /slack/interactive endpoint, and we verify here that
// these IDs do NOT trigger the existing ApproveAction / DenyAction use case
// (they go through DispatchUseCase via the handleDispatchAction branch).
//
// If a future change rewrites the matcher to e.g. strings.Contains(... "approve")
// or HasPrefix(... "dispatch_approve"), this test will catch the resulting
// silent dispatch of an existing ApproveAction.
func TestInteractiveHandler_DispatchActionIDsDoNotRouteToApprove(t *testing.T) {
	cases := []string{"dispatch_approve", "dispatch_deny"}
	for _, actionID := range cases {
		t.Run(actionID, func(t *testing.T) {
			secret := "test-secret"
			mock := newMockUseCase()
			handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

			av := actionValue{
				Project:       "test-project",
				Location:      "asia-northeast1",
				ResourceType:  "service",
				ResourceNames: "svc",
				IssuedAt:      time.Now().Unix(),
			}
			avBytes, _ := json.Marshal(av)

			payload := interactivePayload{}
			payload.User.ID = "U999"
			payload.Actions = []interactiveAction{
				{ActionID: actionID, Value: string(avBytes)},
			}
			payloadBytes, _ := json.Marshal(payload)

			req := buildValidRequest(t, secret, string(payloadBytes))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", rr.Code)
			}
			// ApproveAction MUST NOT be called for dispatch_* IDs.
			select {
			case req := <-mock.approveCh:
				t.Errorf("ApproveAction was called for action_id=%q with req=%+v — Phase 4 routing leaked into Phase 0 ChatOps", actionID, req)
			case req := <-mock.denyCh:
				t.Errorf("DenyAction was called for action_id=%q with req=%+v — Phase 4 routing leaked into Phase 0 ChatOps", actionID, req)
			case <-time.After(50 * time.Millisecond):
				// expected: no use case invocation
			}
		})
	}
}

func TestInteractiveHandler_MalformedActionValue(t *testing.T) {
	// given — action_id is valid but Value is not parseable JSON
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.Actions = []interactiveAction{
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

func TestInteractiveHandler_MultipleActions_OnlyFirstProcessed(t *testing.T) {
	// given — two actions in the payload; only the first must be dispatched
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{Project: "test-project", Location: "asia-northeast1", ResourceType: "service", ResourceNames: "svc", IssuedAt: time.Now().Unix()}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.Actions = []interactiveAction{
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
		Project:       "test-project",
		Location:      "asia-northeast1",
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
		Project:       "test-project",
		Location:      "asia-northeast1",
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

func TestInteractiveHandler_CompressedButtonValue_Dispatched(t *testing.T) {
	// given — button value is gz: compressed (simulates large multi-service bundle)
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{
		Project:       "test-project",
		Location:      "asia-northeast1",
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
	payload.Actions = []interactiveAction{
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

func TestInteractiveHandler_MissingProjectOrLocation_RejectsGracefully(t *testing.T) {
	// given — action value has no project/location; handler must return 200 without dispatching
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{
		ResourceType:  "service",
		ResourceNames: "frontend",
		Targets:       "v2",
		Action:        "canary_10",
		IssuedAt:      time.Now().Unix(),
		// Project and Location intentionally empty
	}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.ResponseURL = "https://hooks.slack.com/response"
	payload.Actions = []interactiveAction{
		{ActionID: "approve", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then — 200 OK but ApproveAction must NOT be called
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	select {
	case <-mock.approveCh:
		t.Error("expected ApproveAction NOT to be called when project/location are missing")
	default:
	}
}

func TestInteractiveHandler_MalformedPayloadJSON(t *testing.T) {
	// given
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	req := buildValidRequest(t, secret, `{not valid json}`)
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestInteractiveHandler_ButtonValueBuildInfo_PropagatesToApprovalRequest(t *testing.T) {
	// given — Slack interactive payload whose action value carries build_info.
	// The handler must surface it on domain.ApprovalRequest so the usecase
	// (and downstream rebuild/progress messages) can show it.
	secret := "test-secret"
	mock := newMockUseCase()
	handler := NewInteractiveHandler(mock, nil, testNotifier, secret)

	av := actionValue{
		Project: "test-project", Location: "asia-northeast1",
		ResourceType: "service", ResourceNames: "frontend", Targets: "v2",
		Action: "canary_10", IssuedAt: time.Now().Unix(),
		BuildInfo: "main @ d948375",
	}
	avBytes, _ := json.Marshal(av)

	payload := interactivePayload{}
	payload.User.ID = "U123"
	payload.ResponseURL = "https://hooks.slack.com/response"
	payload.Actions = []interactiveAction{
		{ActionID: "approve", Value: string(avBytes)},
	}
	payloadBytes, _ := json.Marshal(payload)

	req := buildValidRequest(t, secret, string(payloadBytes))
	rr := httptest.NewRecorder()

	// when
	handler.ServeHTTP(rr, req)

	// then — the BuildInfo from the wire is on the domain request.
	select {
	case got := <-mock.approveCh:
		if got.BuildInfo != "main @ d948375" {
			t.Errorf("BuildInfo = %q, want %q", got.BuildInfo, "main @ d948375")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ApproveAction")
	}
}
