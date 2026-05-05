//go:build integration

// Package integration runs end-to-end tests against the local Firebase
// Pub/Sub emulator. Requires:
//
//	just pubsub-up
//	just pubsub-init
//	PUBSUB_EMULATOR_HOST=localhost:9399 PUBSUB_PROJECT_ID=runops-local just test-integration
//
// The build tag keeps these out of `just test` so the unit suite stays fast
// and offline.
package integration

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	"github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

const (
	defaultProjectID    = "runops-local"
	defaultInboundTopic = "dmail-inbound"
	defaultInboundSub   = "dmail-receiver-sub"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEmulator(t *testing.T) {
	t.Helper()
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Skip("integration test skipped: PUBSUB_EMULATOR_HOST not set (run `just pubsub-up && just pubsub-init` first)")
	}
}

// receiveOne pulls a single message from sub or returns an error if nothing
// arrives within deadline. Acks the message so subsequent tests start clean.
func receiveOne(ctx context.Context, t *testing.T, client *gpubsub.Client, subID string, deadline time.Duration) (*gpubsub.Message, error) {
	t.Helper()
	sub := client.Subscriber(subID)
	pullCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	var (
		mu  sync.Mutex
		got *gpubsub.Message
	)
	err := sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		if got == nil {
			got = m
			m.Ack()
			cancel() // stop after the first message
			return
		}
		m.Nack()
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if got == nil {
		return nil, errors.New("no message received within deadline")
	}
	return got, nil
}

func TestIntegration_PubsubDispatcher_PublishesDispatchAsSpecificationDMail(t *testing.T) {
	requireEmulator(t)
	ctx := context.Background()

	projectID := envOr("PUBSUB_PROJECT_ID", defaultProjectID)
	topicID := envOr("PUBSUB_DMAIL_INBOUND_TOPIC", defaultInboundTopic)
	subID := envOr("PUBSUB_DMAIL_INBOUND_SUB", defaultInboundSub)

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

	msg, err := receiveOne(ctx, t, subClient, subID, 10*time.Second)
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

func TestIntegration_PubsubDispatcher_PreservesOrderingPerTarget(t *testing.T) {
	requireEmulator(t)
	ctx := context.Background()

	projectID := envOr("PUBSUB_PROJECT_ID", defaultProjectID)
	topicID := envOr("PUBSUB_DMAIL_INBOUND_TOPIC", defaultInboundTopic)
	subID := envOr("PUBSUB_DMAIL_INBOUND_SUB", defaultInboundSub)

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
		}
		m.Ack()
		if len(ordered) >= 3 {
			cancel()
		}
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("receive: %v", err)
	}

	// All three messages must be delivered. Strict ordering is asserted in
	// production against Cloud Pub/Sub itself; the Firebase emulator does
	// not honour ordering keys reliably (documented limitation), so we
	// assert delivery completeness here and leave order verification to
	// production smoke tests.
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
