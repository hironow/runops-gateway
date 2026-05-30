//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	slackadapter "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
	testutils "github.com/hironow/runops-gateway/tests/utils"
)

// dropPrimary is a no-op port.Notifier whose UpdateMessage always returns a
// response_url-style 404 so FallbackNotifier always falls through to
// chat.postMessage. We only care about the chat.postMessage call here.
type dropPrimary struct{}

func (dropPrimary) UpdateMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return errors.New("slack notifier: unexpected status 404: expired_url")
}
func (dropPrimary) ReplaceMessage(_ context.Context, _ port.NotifyTarget, _ string) error {
	return errors.New("slack notifier: unexpected status 404: expired_url")
}
func (dropPrimary) SendEphemeral(_ context.Context, _ port.NotifyTarget, _, _ string) error {
	return nil
}
func (dropPrimary) OfferContinuation(_ context.Context, _ port.NotifyTarget, _ string, _, _ *domain.ApprovalRequest) error {
	return nil
}
func (dropPrimary) RebuildInitialApproval(_ context.Context, _ port.NotifyTarget, _ string, _, _, _ *domain.ApprovalRequest) error {
	return nil
}

func TestIntegration_DmailOutbound_PostsToSlackThread(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicOutbound
	subID := testutils.SubGateway

	// 1. Mock Slack chat.postMessage server.
	var (
		chatPostMu    sync.Mutex
		chatPostCalls []map[string]any
	)
	chatPostSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		chatPostMu.Lock()
		chatPostCalls = append(chatPostCalls, body)
		chatPostMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1700000000.000100"})
	}))
	defer chatPostSrv.Close()

	// 2. FallbackNotifier wired against the mock. dropPrimary forces the
	//    fallback path because the response_url is long expired by the time
	//    Phase 3 results come back through Pub/Sub.
	fallback := slackadapter.NewFallbackNotifier(dropPrimary{}, chatPostSrv.URL, "xoxb-int-test", "")
	handler := usecase.NewDispatchResultHandler(fallback)
	receiver := pubsubinput.NewOutboundReceiver(handler)

	// 3. Background StreamingPull on the outbound subscription.
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, testutils.MsgAdapter{Inner: m})
		})
	}()

	// 4. Publish a Phase 3-shaped DMail with all three required metadata
	//    keys plus a unique body marker so we can distinguish it from any
	//    leftovers another integration test might have left behind.
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	stamp := time.Now().Format("150405.000000")
	marker := "outbound-rt " + stamp
	mail := domain.DMail{
		ID:             "out-" + stamp,
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "out-" + stamp,
		Body:           marker,
		Metadata: map[string]string{
			"slack_channel_id":       "C_INT_OUT",
			"slack_thread_ts":        "1700000000.000050",
			"parent_idempotency_key": "parent-" + stamp,
		},
	}
	if _, err := pub.PublishDMail(ctx, mail); err != nil {
		t.Fatalf("PublishDMail: %v", err)
	}

	// 5. Wait for chat.postMessage to be called with our marker.
	deadline := time.Now().Add(15 * time.Second)
	var hit map[string]any
	for time.Now().Before(deadline) && hit == nil {
		chatPostMu.Lock()
		for _, c := range chatPostCalls {
			if t, ok := c["text"].(string); ok && strings.Contains(t, marker) {
				hit = c
				break
			}
		}
		chatPostMu.Unlock()
		if hit == nil {
			time.Sleep(150 * time.Millisecond)
		}
	}
	if hit == nil {
		t.Fatalf("chat.postMessage was not called with marker %q within deadline (got %d calls)",
			marker, len(chatPostCalls))
	}
	if got, _ := hit["channel"].(string); got != "C_INT_OUT" {
		t.Errorf("channel mismatch: %v", hit["channel"])
	}
	if got, _ := hit["thread_ts"].(string); got != "1700000000.000050" {
		t.Errorf("thread_ts mismatch: %v", hit["thread_ts"])
	}
	text, _ := hit["text"].(string)
	if !strings.Contains(text, "✅") || !strings.Contains(text, "paintress") || !strings.Contains(text, "amadeus") {
		t.Errorf("rendered text missing emoji or source/target header: %q", text)
	}

	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop: %v", err)
	}
}
