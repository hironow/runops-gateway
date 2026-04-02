package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

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
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}

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
	target := port.NotifyTarget{ResponseURL: "", Mode: "stdout"}

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
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "hello"}}}

	// when
	err := n.ReplaceMessage(context.Background(), target, blocks)

	// then
	if err != nil {
		t.Fatal(err)
	}
	if received["replace_original"] != true {
		t.Error("replace_original must be true")
	}
}

func TestReplaceMessage_StdoutMode(t *testing.T) {
	// given
	n := NewResponseURLNotifier()
	target := port.NotifyTarget{ResponseURL: "", Mode: "stdout"}

	// when
	err := n.ReplaceMessage(context.Background(), target, []string{"block1"})

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
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}

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
	target := port.NotifyTarget{ResponseURL: "", Mode: "stdout"}

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
	target := port.NotifyTarget{ResponseURL: "", Mode: "slack"}

	// when
	err := n.UpdateMessage(context.Background(), target, "hello")

	// then
	if err == nil {
		t.Fatal("expected error for empty response_url, got nil")
	}
}

func TestOfferContinuation_TooLongButtonValue_SendsErrorMessage(t *testing.T) {
	// given — resource_names large enough to push marshalActionValue over 2,000 chars
	longNames := strings.Repeat("x", 600) + "," + strings.Repeat("y", 600)
	nextReq := &domain.ApprovalRequest{
		ResourceType:     domain.ResourceTypeService,
		ResourceNames:    longNames,
		Targets:          longNames,
		Action:           "canary_10",
		IssuedAt:         1700000000,
		NextServiceNames: longNames,
		NextRevisions:    longNames,
		NextAction:       "canary_30",
	}

	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}

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
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}

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

func TestUpdateMessage_ServerError(t *testing.T) {
	// given
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := NewResponseURLNotifier()
	target := port.NotifyTarget{ResponseURL: srv.URL, Mode: "slack"}

	// when
	err := n.UpdateMessage(context.Background(), target, "hello")

	// then
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}
