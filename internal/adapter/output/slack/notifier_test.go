package slack

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// incompressibleNames generates n service names from SHA-256 hashes.
// SHA-256 hex output has near-maximum entropy so gzip cannot compress it meaningfully,
// ensuring the button value exceeds maxButtonValue even after compression.
func incompressibleNames(n int) (names, revisions string) {
	nameParts := make([]string, n)
	revParts := make([]string, n)
	for i := 0; i < n; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("svc%d", i)))
		nameParts[i] = fmt.Sprintf("%x", h[:24]) // 48 hex chars
		h2 := sha256.Sum256([]byte(fmt.Sprintf("rev%d", i)))
		revParts[i] = fmt.Sprintf("%x", h2[:28]) // 56 hex chars
	}
	return strings.Join(nameParts, ","), strings.Join(revParts, ",")
}

// compile-time interface assertion
var _ port.Notifier = (*ResponseURLNotifier)(nil)

func TestUpdateMessage_SlackMode_Success(t *testing.T) {
	// given
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "hello")

	// then
	if err != nil {
		t.Fatal(err)
	}
	if received["replace_original"] != true {
		t.Error("replace_original must be true")
	}
	if received["text"] != "hello" {
		t.Errorf("text=%v", received["text"])
	}
}

func TestUpdateMessage_StdoutMode(t *testing.T) {
	// given
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeStdout}

	// when
	err := n.UpdateMessage(context.Background(), target, "stdout message")

	// then
	if err != nil {
		t.Fatalf("stdout mode should not error, got: %v", err)
	}
}

func TestReplaceMessage_SlackMode_ReplaceOriginalTrue(t *testing.T) {
	// given
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.ReplaceMessage(context.Background(), target, "hello")

	// then
	if err != nil {
		t.Fatal(err)
	}
	if received["replace_original"] != true {
		t.Error("replace_original must be true")
	}
	// Verify it sends blocks (section block), not plain text
	blocks, ok := received["blocks"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatal("expected blocks array with section block")
	}
	section, ok := blocks[0].(map[string]any)
	if !ok || section["type"] != "section" {
		t.Error("expected section block")
	}
}

func TestReplaceMessage_StdoutMode(t *testing.T) {
	// given
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeStdout}

	// when
	err := n.ReplaceMessage(context.Background(), target, "stdout message")

	// then
	if err != nil {
		t.Fatalf("stdout mode should not error, got: %v", err)
	}
}

func TestSendEphemeral_SlackMode_EphemeralPayload(t *testing.T) {
	// given
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.SendEphemeral(context.Background(), target, "U123", "please check")

	// then
	if err != nil {
		t.Fatal(err)
	}
	if received["response_type"] != "ephemeral" {
		t.Errorf("response_type=%v, want ephemeral", received["response_type"])
	}
	if received["replace_original"] != false {
		t.Errorf("replace_original must be false, got %v", received["replace_original"])
	}
}

func TestSendEphemeral_StdoutMode(t *testing.T) {
	// given
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeStdout}

	// when
	err := n.SendEphemeral(context.Background(), target, "U123", "please check")

	// then
	if err != nil {
		t.Fatalf("stdout mode should not error, got: %v", err)
	}
}

func TestUpdateMessage_EmptyResponseURL(t *testing.T) {
	// given
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "hello")

	// then
	if err == nil {
		t.Fatal("expected error for empty response_url, got nil")
	}
}

func TestOfferContinuation_TooLongButtonValue_SendsErrorMessage(t *testing.T) {
	// given — 30 services with SHA-256-derived names (high entropy → gzip cannot compress)
	// so the button value stays over maxButtonValue (2,000 chars) even after compression.
	longNames, longRevs := incompressibleNames(30)
	nextReq := &domain.ApprovalRequest{
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: longNames,
		Targets:       longRevs,
		Action:        "canary_10",
		IssuedAt:      1700000000,
	}

	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then — no HTTP error (message was sent successfully)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Must have sent a text error message, not a blocks payload with buttons
	text, ok := received["text"].(string)
	if !ok || text == "" {
		t.Fatal("expected error text message, got none")
	}
	if !strings.Contains(text, "⚠️") {
		t.Errorf("expected warning sign in error message, got: %s", text)
	}
	if _, hasBlocks := received["blocks"]; hasBlocks {
		t.Error("error fallback must not include blocks (no broken buttons)")
	}
}

func TestOfferContinuation_NormalLength_SendsBlocksMessage(t *testing.T) {
	// given — short request well within button value limit
	nextReq := &domain.ApprovalRequest{
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-service-00001-abc",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}

	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then — normal blocks message with buttons
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, hasBlocks := received["blocks"]; !hasBlocks {
		t.Error("expected blocks in normal continuation message")
	}
}

func TestOfferContinuation_StdoutMode_NoNextReq(t *testing.T) {
	// given — stdout mode, no next step
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeStdout}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 完了", nil, nil)

	// then — stdout mode must not error
	if err != nil {
		t.Fatalf("stdout mode should not error, got: %v", err)
	}
}

func TestOfferContinuation_StdoutMode_WithNextReq(t *testing.T) {
	// given — stdout mode with a next step (exercises the nextReq != nil log branch)
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: "", Mode: port.ModeStdout}
	nextReq := &domain.ApprovalRequest{
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 10%完了", nextReq, nil)

	// then — no error, no HTTP call made
	if err != nil {
		t.Fatalf("stdout mode should not error, got: %v", err)
	}
}

func TestUpdateMessage_ServerError(t *testing.T) {
	// given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "hello")

	// then
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

// --- Slack response_url response pattern tests ---
// These tests verify every known Slack response pattern to prevent silent failures.

func TestPost_200Ok_NoError(t *testing.T) {
	// given — Slack returns 200 with body "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "test")

	// then
	if err != nil {
		t.Fatalf("200 ok should not error, got: %v", err)
	}
}

func TestPost_200InvalidBlocks_NoErrorButLogsWarning(t *testing.T) {
	// given — Slack returns 200 with body "invalid_blocks" (bad Block Kit payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid_blocks"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when — current implementation logs but does not error
	err := n.UpdateMessage(context.Background(), target, "test")

	// then — no HTTP-level error (Slack returned 200)
	if err != nil {
		t.Fatalf("200 invalid_blocks should not return error, got: %v", err)
	}
}

func TestPost_404_ReturnsErrorWithBody(t *testing.T) {
	// given — Slack returns 404 (response_url expired or invalid)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("expired_url"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "test")

	// then
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain status code 404, got: %v", err)
	}
	if !strings.Contains(err.Error(), "expired_url") {
		t.Errorf("error should contain response body, got: %v", err)
	}
}

func TestPost_503_ReturnsErrorWithBody(t *testing.T) {
	// given — Slack returns 503 service unavailable
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service_unavailable"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "test")

	// then
	if err == nil {
		t.Fatal("expected error for 503 response, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should contain status code 503, got: %v", err)
	}
}

func TestPost_ConnectionRefused_ReturnsError(t *testing.T) {
	// given — server is shut down (connection refused)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // shut down immediately

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: url, Mode: port.ModeSlack}

	// when
	err := n.UpdateMessage(context.Background(), target, "test")

	// then
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

// --- OfferContinuation Slack response tests ---

func TestOfferContinuation_SlackReturns404_ReturnsError(t *testing.T) {
	// given — simulates the OfferContinuation 404 bug
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("expired_url"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}
	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain 404, got: %v", err)
	}
}

func TestOfferContinuation_SlackReturnsInvalidBlocks_NoError(t *testing.T) {
	// given — Slack accepts the POST (200) but rejects the blocks
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid_blocks"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}
	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then — 200 response, no HTTP error
	if err != nil {
		t.Fatalf("200 invalid_blocks should not error, got: %v", err)
	}
}

// --- Sequential call pattern tests (UpdateMessage ok → OfferContinuation fails) ---

func TestUpdateMessageOk_ThenOfferContinuation404(t *testing.T) {
	// given — mock server: first request returns 200, second returns 404
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("expired_url"))
		}
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when — UpdateMessage succeeds
	err1 := n.UpdateMessage(context.Background(), target, "⏳ 処理中...")

	// then
	if err1 != nil {
		t.Fatalf("UpdateMessage should succeed, got: %v", err1)
	}

	// when — OfferContinuation fails (404)
	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	err2 := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then — second call fails
	if err2 == nil {
		t.Fatal("OfferContinuation should fail with 404, got nil")
	}
	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", callCount)
	}
}

func TestUpdateMessageOk_ThenOfferContinuation500(t *testing.T) {
	// given — first request ok, second 500
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal_error"))
		}
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err1 := n.UpdateMessage(context.Background(), target, "⏳ 処理中...")
	if err1 != nil {
		t.Fatalf("UpdateMessage should succeed, got: %v", err1)
	}

	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	err2 := n.OfferContinuation(context.Background(), target, "✅ 完了", nextReq, nil)

	// then
	if err2 == nil {
		t.Fatal("OfferContinuation should fail with 500, got nil")
	}
	if !strings.Contains(err2.Error(), "500") {
		t.Errorf("error should contain 500, got: %v", err2)
	}
}

// --- OfferContinuation payload structure verification ---

func TestOfferContinuation_PayloadContainsBlocksWithButtons(t *testing.T) {
	// given
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	nextReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "canary_30",
		IssuedAt:      1700000000,
	}
	stopReq := &domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-v2",
		Action:        "rollback",
		IssuedAt:      1700000000,
	}

	// when
	err := n.OfferContinuation(context.Background(), target, "✅ 10%完了", nextReq, stopReq)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify replace_original is set
	if received["replace_original"] != true {
		t.Error("replace_original must be true")
	}

	// Verify blocks structure
	blocks, ok := received["blocks"].([]any)
	if !ok {
		t.Fatal("expected blocks array in payload")
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 blocks (section + actions), got %d", len(blocks))
	}

	// First block should be section with summary
	section, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatal("first block should be a map")
	}
	if section["type"] != "section" {
		t.Errorf("first block type=%v, want section", section["type"])
	}

	// Last block should be actions with buttons
	actionsBlock, ok := blocks[len(blocks)-1].(map[string]any)
	if !ok {
		t.Fatal("last block should be a map")
	}
	if actionsBlock["type"] != "actions" {
		t.Errorf("last block type=%v, want actions", actionsBlock["type"])
	}

	elements, ok := actionsBlock["elements"].([]any)
	if !ok {
		t.Fatal("actions block should have elements array")
	}
	if len(elements) != 2 {
		t.Fatalf("expected 2 buttons (next + stop), got %d", len(elements))
	}

	// Verify next button has compressed value
	nextBtn, ok := elements[0].(map[string]any)
	if !ok {
		t.Fatal("first button should be a map")
	}
	nextValue, ok := nextBtn["value"].(string)
	if !ok || nextValue == "" {
		t.Fatal("next button must have a non-empty value")
	}
	if !strings.HasPrefix(nextValue, "gz:") {
		t.Errorf("button value should be gzip-compressed (gz: prefix), got: %.20s...", nextValue)
	}

	// Verify stop button has compressed value
	stopBtn, ok := elements[1].(map[string]any)
	if !ok {
		t.Fatal("second button should be a map")
	}
	stopValue, ok := stopBtn["value"].(string)
	if !ok || stopValue == "" {
		t.Fatal("stop button must have a non-empty value")
	}
	if !strings.HasPrefix(stopValue, "gz:") {
		t.Errorf("stop button value should be gzip-compressed (gz: prefix), got: %.20s...", stopValue)
	}
}

func TestOfferContinuation_NoNextReq_NoActionsBlock(t *testing.T) {
	// given — no next step (final canary at 100%)
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when — nextReq and stopReq are both nil
	err := n.OfferContinuation(context.Background(), target, "✅ 100%完了", nil, nil)

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	blocks, ok := received["blocks"].([]any)
	if !ok {
		t.Fatal("expected blocks array")
	}
	// Should have only section block, no actions block
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "actions" {
			t.Error("no actions block expected when nextReq is nil")
		}
	}
}

// --- ReplaceMessage payload tests (deny flow) ---

func TestReplaceMessage_PayloadIsBlocksArray(t *testing.T) {
	// given — verify that ReplaceMessage builds section block from text (completionBlocks bug prevention)
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when — pass text, adapter wraps in section block
	err := n.ReplaceMessage(context.Background(), target, ":x: 拒否されました")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received["replace_original"] != true {
		t.Error("replace_original must be true")
	}
	// blocks should be an array with one section block
	receivedBlocks, ok := received["blocks"].([]any)
	if !ok {
		t.Fatalf("blocks should be an array, got: %T", received["blocks"])
	}
	if len(receivedBlocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(receivedBlocks))
	}
	// Verify the block is a section with mrkdwn text
	section, ok := receivedBlocks[0].(map[string]any)
	if !ok {
		t.Fatal("expected section block to be a map")
	}
	if section["type"] != "section" {
		t.Errorf("expected section type, got %v", section["type"])
	}
	textObj, ok := section["text"].(map[string]any)
	if !ok {
		t.Fatal("expected text object in section")
	}
	if textObj["type"] != "mrkdwn" {
		t.Errorf("expected mrkdwn text type, got %v", textObj["type"])
	}
	if textObj["text"] != ":x: 拒否されました" {
		t.Errorf("text mismatch: got %v", textObj["text"])
	}
}

func TestReplaceMessage_404_ReturnsError(t *testing.T) {
	// given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("expired_url"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.ReplaceMessage(context.Background(), target, "test")

	// then
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should contain 404, got: %v", err)
	}
}

// --- SendEphemeral Slack response tests ---

func TestSendEphemeral_404_ReturnsError(t *testing.T) {
	// given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("expired_url"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	err := n.SendEphemeral(context.Background(), target, "U123", "test")

	// then
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

// --- Content-Type verification ---

func TestPost_SendsApplicationJsonContentType(t *testing.T) {
	// given
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

	// when
	_ = n.UpdateMessage(context.Background(), target, "test")

	// then
	if contentType != "application/json" {
		t.Errorf("Content-Type=%q, want application/json", contentType)
	}
}
