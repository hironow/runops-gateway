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

	phonewaveinput "github.com/hironow/runops-gateway/internal/adapter/input/phonewave"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

const (
	defaultOutboundTopic = "dmail-outbound"
	defaultOutboundSub   = "runops-gateway-sub"
)

func TestIntegration_DmailEmitter_PublishesArchivedFiles(t *testing.T) {
	requireEmulator(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := envOr("PUBSUB_PROJECT_ID", defaultProjectID)
	topicID := envOr("PUBSUB_DMAIL_OUTBOUND_TOPIC", defaultOutboundTopic)
	subID := envOr("PUBSUB_DMAIL_OUTBOUND_SUB", defaultOutboundSub)

	// 1. Publisher pointed at the outbound topic.
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	// 2. Watcher driving the emitter on a temp archive dir.
	archive := t.TempDir()
	emitter := phonewaveinput.NewEmitter(pub)
	watcher := phonewaveinput.NewWatcher(emitter, archive)

	runDone := make(chan error, 1)
	go func() { runDone <- watcher.Run(ctx) }()
	time.Sleep(200 * time.Millisecond) // let fsnotify subscribe

	// 3. Drop a real DMail .md into the archive.
	stamp := time.Now().Format("150405.000000")
	bodyMarker := "emit-rt " + stamp
	mail := domain.DMail{
		ID:             "em-" + stamp,
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "em-" + stamp,
		Body:           bodyMarker,
	}
	path := filepath.Join(archive, "msg-"+stamp+".md")
	if err := os.WriteFile(path, []byte(mail.RenderMarkdown()), 0o644); err != nil {
		t.Fatalf("write archive .md: %v", err)
	}

	// 4. Subscribe to the outbound topic and wait for our marker to appear.
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	pullCtx, pullCancel := context.WithTimeout(ctx, 15*time.Second)
	defer pullCancel()

	got := make(chan struct{}, 1)
	sub := subClient.Subscriber(subID)
	go func() {
		_ = sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
			defer m.Ack()
			if strings.Contains(string(m.Data), bodyMarker) {
				select {
				case got <- struct{}{}:
				default:
				}
			}
		})
	}()

	select {
	case <-got:
		// success
	case <-time.After(15 * time.Second):
		t.Fatalf("emitter did not publish marker %q within deadline", bodyMarker)
	}

	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("watcher returned unexpected error: %v", err)
	}
}
