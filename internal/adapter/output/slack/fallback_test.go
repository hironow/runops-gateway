package slack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// stubNotifier records every call and reports a configurable error so tests can
// drive the FallbackNotifier through specific failure paths.
type stubPrimaryNotifier struct {
	mu             sync.Mutex
	updateCalls    int
	replaceCalls   int
	ephemeralCalls int
	offerCalls     int
	rebuildCalls   int
	updateErr      error
	replaceErr     error
	ephemeralErr   error
	offerErr       error
	rebuildErr     error
}

func (s *stubPrimaryNotifier) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalls++
	return s.updateErr
}

func (s *stubPrimaryNotifier) ReplaceMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaceCalls++
	return s.replaceErr
}

func (s *stubPrimaryNotifier) SendEphemeral(_ context.Context, _ port.NotifyTarget, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ephemeralCalls++
	return s.ephemeralErr
}

func (s *stubPrimaryNotifier) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, _, _ *domain.ApprovalRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offerCalls++
	return s.offerErr
}

func (s *stubPrimaryNotifier) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, _ string, _, _, _ *domain.ApprovalRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebuildCalls++
	return s.rebuildErr
}

// recordingChatPostHandler returns an httptest server that records every
// chat.postMessage call and replies with a JSON-serialized reply value.
//
// We pass a Go map for the reply (not a raw string) so the response goes
// through json.Encoder, side-stepping the semgrep rule that flags any
// untyped string write to http.ResponseWriter.
func recordingChatPostHandler(t *testing.T, reply map[string]any) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var posts []map[string]any
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		posts = append(posts, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if reply == nil {
			reply = map[string]any{"ok": true}
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func okReply() map[string]any {
	return map[string]any{"ok": true, "ts": "1700000000.000100"}
}

// --- compile-time interface assertion ---

var _ port.Notifier = (*FallbackNotifier)(nil)

// --- tests ---

func TestFallbackNotifier_PassesThroughWhenPrimarySucceeds(t *testing.T) {
	primary := &stubPrimaryNotifier{}
	srv, posts := recordingChatPostHandler(t, okReply())
	fb := NewFallbackNotifier(primary, srv.URL, "xoxb-test", "C123")

	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}

	if err := fb.UpdateMessage(context.Background(), target, "hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if primary.updateCalls != 1 {
		t.Errorf("primary should be called exactly once on success, got %d", primary.updateCalls)
	}
	if len(*posts) != 0 {
		t.Errorf("chat.postMessage must not be invoked when primary succeeds; got %d posts", len(*posts))
	}
}

func TestFallbackNotifier_FallsBackOnResponseURLLimitError(t *testing.T) {
	// Primary fails with a response_url-style 404 ("expired_url" / 5-call limit).
	primary := &stubPrimaryNotifier{
		updateErr: errors.New("slack notifier: unexpected status 404: expired_url"),
	}
	srv, posts := recordingChatPostHandler(t, okReply())
	fb := NewFallbackNotifier(primary, srv.URL, "xoxb-test", "C123")

	target := port.NotifyTarget{
		CallbackURL: "https://hooks.slack.com/x",
		Mode:        port.ModeSlack,
		ChannelID:   "C123",
		ThreadTS:    "1700000000.000050",
	}
	if err := fb.UpdateMessage(context.Background(), target, "hello"); err != nil {
		t.Fatalf("expected fallback to recover, got: %v", err)
	}
	if len(*posts) != 1 {
		t.Fatalf("chat.postMessage should be called once after primary 404; got %d", len(*posts))
	}
	post := (*posts)[0]
	if post["channel"] != "C123" {
		t.Errorf("channel mismatch: got %v", post["channel"])
	}
	if post["thread_ts"] != "1700000000.000050" {
		t.Errorf("thread_ts mismatch: got %v", post["thread_ts"])
	}
	if post["text"] != "hello" {
		t.Errorf("text mismatch: got %v", post["text"])
	}
}

func TestFallbackNotifier_DoesNotFallbackOnUnrelatedErrors(t *testing.T) {
	primary := &stubPrimaryNotifier{
		updateErr: errors.New("slack notifier: post: connection refused"),
	}
	srv, posts := recordingChatPostHandler(t, okReply())
	fb := NewFallbackNotifier(primary, srv.URL, "xoxb-test", "C123")

	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}
	err := fb.UpdateMessage(context.Background(), target, "hello")
	if err == nil {
		t.Fatal("expected non-limit error to propagate, got nil")
	}
	if len(*posts) != 0 {
		t.Errorf("chat.postMessage must not run for non-limit errors; got %d posts", len(*posts))
	}
}

func TestFallbackNotifier_RequiresChannelIDForFallback(t *testing.T) {
	// If the target lacks ChannelID we cannot post via chat.postMessage; the
	// fallback must surface an explicit error rather than silently dropping.
	primary := &stubPrimaryNotifier{
		updateErr: errors.New("slack notifier: unexpected status 404: rate_limited"),
	}
	srv, _ := recordingChatPostHandler(t, okReply())
	fb := NewFallbackNotifier(primary, srv.URL, "xoxb-test", "")

	// target.ChannelID empty
	target := port.NotifyTarget{CallbackURL: "https://hooks.slack.com/x", Mode: port.ModeSlack}
	err := fb.UpdateMessage(context.Background(), target, "hello")
	if err == nil {
		t.Fatal("expected error when no channel is available for fallback, got nil")
	}
	if !strings.Contains(err.Error(), "channel") {
		t.Errorf("error should mention missing channel; got: %v", err)
	}
}

func TestFallbackNotifier_FallsBackOnOfferContinuation(t *testing.T) {
	// OfferContinuation is the path Issue 0017 cites — make sure the same
	// fallback chain catches it.
	primary := &stubPrimaryNotifier{
		offerErr: errors.New("slack notifier: unexpected status 404: expired_url"),
	}
	srv, posts := recordingChatPostHandler(t, okReply())
	fb := NewFallbackNotifier(primary, srv.URL, "xoxb-test", "C123")

	target := port.NotifyTarget{
		CallbackURL: "https://hooks.slack.com/x",
		Mode:        port.ModeSlack,
		ChannelID:   "C123",
		ThreadTS:    "1700000000.000050",
	}
	err := fb.OfferContinuation(context.Background(), target, "summary", nil, nil)
	if err != nil {
		t.Fatalf("expected fallback recovery, got: %v", err)
	}
	if len(*posts) != 1 {
		t.Fatalf("expected one chat.postMessage call from OfferContinuation fallback; got %d", len(*posts))
	}
}

func TestFallbackNotifier_StdoutModePassesThrough(t *testing.T) {
	// stdout mode (CLI) must keep working without ever hitting Slack.
	primary := &stubPrimaryNotifier{}
	fb := NewFallbackNotifier(primary, "https://invalid.example", "xoxb-test", "C123")

	target := port.NotifyTarget{Mode: port.ModeStdout}
	if err := fb.UpdateMessage(context.Background(), target, "hello"); err != nil {
		t.Errorf("stdout mode must succeed via primary: %v", err)
	}
	if primary.updateCalls != 1 {
		t.Errorf("primary should still be called for stdout mode")
	}
}
