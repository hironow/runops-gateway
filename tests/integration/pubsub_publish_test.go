//go:build integration

// Package integration runs end-to-end tests against a firebase Pub/Sub
// emulator started by testcontainers (see setup_test.go's TestMain). The tests
// depend ONLY on testcontainers — no locally-running emulator, no external
// PUBSUB_EMULATOR_HOST, no docker compose. The build tag keeps these out of
// `just test` so the unit suite stays fast and offline.
package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	"github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	"github.com/hironow/runops-gateway/internal/core/domain"
	testutils "github.com/hironow/runops-gateway/tests/utils"
)

func TestIntegration_PubsubDispatcher_PublishesDispatchAsSpecificationDMail(t *testing.T) {
	ctx := context.Background()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	d := dispatcher.NewPubsubDispatcher(pub)

	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           "Fix M-42 in the auth module.",
		RequesterID:    "U_INT_TEST",
		IdempotencyKey: "intkey-" + time.Now().Format("150405.000000"),
		IssuedAt:       time.Now().Unix(),
	}
	if err := d.Dispatch(ctx, req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	msg, err := testutils.ReceiveOne(ctx, t, subClient, subID, 10*time.Second)
	if err != nil {
		t.Fatalf("did not receive published message: %v", err)
	}

	wantAttrs := map[string]string{
		"kind":                 "specification",
		"target_tool":          "paintress",
		"source":               "runops-gateway-slack",
		"dmail_schema_version": "1",
		"idempotency_key":      req.IdempotencyKey,
	}
	for k, want := range wantAttrs {
		if got := msg.Attributes[k]; got != want {
			t.Errorf("attribute %s: got %q want %q", k, got, want)
		}
	}
	if got := msg.Attributes["requester_id"]; got != req.RequesterID {
		t.Errorf("attribute requester_id: got %q want %q", got, req.RequesterID)
	}
	body := string(msg.Data)
	for _, must := range []string{
		"dmail-schema-version: \"1\"",
		"kind: specification",
		"target: paintress",
		"source: runops-gateway-slack",
		"Fix M-42 in the auth module.",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("body missing %q; got:\n%s", must, body)
		}
	}
}

func TestIntegration_PubsubDispatcher_DeliversAllPerTarget(t *testing.T) {
	ctx := context.Background()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	d := dispatcher.NewPubsubDispatcher(pub)

	// Publish three dispatches against the same target_tool with a shared
	// "batch" prefix so the receiver test can assert order even when other
	// publishers (parallel test runs) interleave their own messages.
	batch := "batch-" + time.Now().Format("150405.000000")
	for i, body := range []string{batch + "-A", batch + "-B", batch + "-C"} {
		if err := d.Dispatch(ctx, domain.DispatchRequest{
			Role:           domain.AgentRoleSightjack,
			Text:           body,
			RequesterID:    "U_INT_ORDERING",
			IdempotencyKey: batch + "-" + string(rune('0'+i)),
			IssuedAt:       time.Now().Unix(),
		}); err != nil {
			t.Fatalf("Dispatch #%d: %v", i, err)
		}
	}

	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	pullCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	var (
		mu      sync.Mutex
		ordered []string
	)
	sub := subClient.Subscriber(subID)
	err = sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		// Filter to our batch only — other tests may share the topic.
		if !strings.Contains(string(m.Data), batch+"-") {
			m.Nack()
			return
		}
		switch {
		case strings.Contains(string(m.Data), batch+"-A"):
			ordered = append(ordered, "A")
		case strings.Contains(string(m.Data), batch+"-B"):
			ordered = append(ordered, "B")
		case strings.Contains(string(m.Data), batch+"-C"):
			ordered = append(ordered, "C")
		default:
			// Carries our batch prefix but is none of A/B/C: the only way this
			// fires is corrupted/extra test data, which would otherwise inflate
			// the completeness count silently. Fail loud instead.
			t.Errorf("in-batch message matched prefix but not A/B/C: %q", string(m.Data))
		}
		m.Ack()
		if len(ordered) >= 3 {
			cancel()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("receive: %v", err)
	}

	// This test asserts delivery COMPLETENESS, not order. Strict per-target
	// ordering is a production-only guarantee of Cloud Pub/Sub (it requires
	// ordering keys plus a single ordering region); the integration suite
	// deliberately does NOT assert order against the emulator, so this test
	// verifies only that all three dispatches are delivered. The function name
	// reflects that scope on purpose — it does not claim to preserve order.
	if len(ordered) != 3 {
		t.Fatalf("expected 3 in-batch messages, got %v", ordered)
	}
	seen := map[string]bool{}
	for _, v := range ordered {
		seen[v] = true
	}
	for _, want := range []string{"A", "B", "C"} {
		if !seen[want] {
			t.Errorf("missing message %q in delivery (got %v)", want, ordered)
		}
	}
}
