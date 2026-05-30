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
	"github.com/hironow/runops-gateway/internal/usecase"
	testutils "github.com/hironow/runops-gateway/tests/utils"
)

// TestIntegration_HighSeverityConvergence_PostsApprovalRequestBlocks runs the
// Phase 4a 4-eyes producer side end-to-end: a HIGH severity convergence
// DMail published to dmail-outbound is received by OutboundReceiver, fans out
// to DispatchResultHandler with an ApprovalRequester wired in, and ends up as
// a real chat.postMessage call carrying a Block Kit approval request payload.
func TestIntegration_HighSeverityConvergence_PostsApprovalRequestBlocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicOutbound
	subID := testutils.SubGateway

	// 1. Mock Slack chat.postMessage server records every POST.
	var (
		mu    sync.Mutex
		posts []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posts = append(posts, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "ts": "1700000000.000100"})
	}))
	defer srv.Close()

	// 2. ApprovalRequester wired against the mock + DispatchResultHandler with
	//    it injected. The notifier slot receives a no-op (we are exercising the
	//    HIGH severity branch which never hits the regular notifier path).
	approver := slackadapter.NewApprovalRequester(srv.URL, "xoxb-int-test")
	notifier := dropPrimary{}
	handler := usecase.NewDispatchResultHandler(notifier).WithApprovalRequester(approver)
	receiver := pubsubinput.NewOutboundReceiver(handler)

	// 3. Background StreamingPull on dmail-outbound.
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

	// 4. Publish a HIGH severity convergence DMail.
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	stamp := time.Now().Format("150405.000000")
	bodyMarker := "approval-rt " + stamp
	mail := domain.DMail{
		ID:             "approval-" + stamp,
		Kind:           domain.DMailKindConvergence,
		Target:         "sightjack",
		Source:         "amadeus",
		IdempotencyKey: "approval-" + stamp,
		Body:           bodyMarker,
		Metadata: map[string]string{
			"slack_channel_id":       "C_INT_APR",
			"slack_thread_ts":        "1700000000.000050",
			"parent_idempotency_key": "parent-" + stamp,
			"requester_id":           "U_INT_ORIG",
			"severity":               "high",
		},
	}
	if _, err := pub.PublishDMail(ctx, mail); err != nil {
		t.Fatalf("PublishDMail: %v", err)
	}

	// 5. Wait for chat.postMessage to be called and look for the approval
	//    request shape (channel + blocks + Approve button).
	deadline := time.Now().Add(15 * time.Second)
	var hit map[string]any
	for time.Now().Before(deadline) && hit == nil {
		mu.Lock()
		for _, p := range posts {
			if got, _ := p["channel"].(string); got != "C_INT_APR" {
				continue
			}
			if blocks, _ := p["blocks"].([]any); len(blocks) > 0 {
				blob, _ := json.Marshal(p)
				if strings.Contains(string(blob), "approval_approve") &&
					strings.Contains(string(blob), bodyMarker) {
					hit = p
					break
				}
			}
		}
		mu.Unlock()
		if hit == nil {
			time.Sleep(150 * time.Millisecond)
		}
	}
	if hit == nil {
		t.Fatalf("approval Block Kit was not posted within deadline (got %d total chat.postMessage calls)", len(posts))
	}
	if got, _ := hit["thread_ts"].(string); got != "1700000000.000050" {
		t.Errorf("thread_ts mismatch: %v", hit["thread_ts"])
	}

	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop: %v", err)
	}
}
