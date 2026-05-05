//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	"github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	"github.com/hironow/runops-gateway/internal/adapter/output/phonewave"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// pubsubMsgAdapter adapts *pubsub.Message to pubsubinput.Message.
type pubsubMsgAdapter struct{ inner *gpubsub.Message }

func (m pubsubMsgAdapter) ID() string                    { return m.inner.ID }
func (m pubsubMsgAdapter) Data() []byte                  { return m.inner.Data }
func (m pubsubMsgAdapter) Attributes() map[string]string { return m.inner.Attributes }
func (m pubsubMsgAdapter) Ack()                          { m.inner.Ack() }
func (m pubsubMsgAdapter) Nack()                         { m.inner.Nack() }

func TestIntegration_DispatchPublish_ReceivedAndWrittenToOutbox(t *testing.T) {
	requireEmulator(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := envOr("PUBSUB_PROJECT_ID", defaultProjectID)
	topicID := envOr("PUBSUB_DMAIL_INBOUND_TOPIC", defaultInboundTopic)
	subID := envOr("PUBSUB_DMAIL_INBOUND_SUB", defaultInboundSub)

	// 1. Spin up the receiver against a temp outbox dir.
	outboxDir := t.TempDir()
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	writer := phonewave.NewOutboxWriter(outboxDir)
	receiver := pubsubinput.NewReceiver(writer)

	// Run the receive loop in the background — cancel via ctx when we are done.
	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, pubsubMsgAdapter{inner: m})
		})
	}()

	// 2. Publish a dispatch through the production publisher path.
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()
	d := dispatcher.NewPubsubDispatcher(pub)

	stamp := time.Now().Format("150405.000000")
	body := "round-trip body " + stamp
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           body,
		RequesterID:    "U_INT_RT",
		IdempotencyKey: "rt-" + stamp,
		IssuedAt:       time.Now().Unix(),
	}
	if err := d.Dispatch(ctx, req); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	// 3. Wait for an outbox file containing our marker to appear.
	deadline := time.Now().Add(15 * time.Second)
	var matched string
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(outboxDir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(outboxDir, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(data), body) {
				matched = e.Name()
				break
			}
		}
		if matched != "" {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if matched == "" {
		t.Fatalf("no outbox file contained marker %q within deadline; dir=%s", body, outboxDir)
	}
	if !strings.HasSuffix(matched, ".md") {
		t.Errorf("outbox file should end with .md, got %q", matched)
	}

	// 4. Cancel the receive loop and wait for clean shutdown.
	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop returned unexpected error: %v", err)
	}
}
