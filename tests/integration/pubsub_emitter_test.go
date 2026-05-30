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
	testutils "github.com/hironow/runops-gateway/tests/utils"
)

func TestIntegration_DmailEmitter_PublishesArchivedFiles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicOutbound
	subID := testutils.SubGateway

	// 1. Publisher pointed at the outbound topic.
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	// 2. Watcher driving the emitter on a temp archive dir.
	archive := t.TempDir()
	emitter := phonewaveinput.NewEmitter(pub, phonewaveinput.NewSingleArchiveRouter())
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

// TestIntegration_DmailEmitter_MultiModeAttachesProjectIDAttribute exercises
// the path → Pub/Sub project_id attribute pipeline end-to-end (#0007). A
// file dropped into the registered archive dir surfaces with project_id=foo
// on the published Pub/Sub message, mirroring the receiver-side multi-mode
// gating from #0006.
func TestIntegration_DmailEmitter_MultiModeAttachesProjectIDAttribute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicOutbound
	subID := testutils.SubGateway

	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	archive := t.TempDir() // foo's archive
	router, err := phonewaveinput.NewMultiArchiveRouter(map[string]string{
		"foo": archive,
	})
	if err != nil {
		t.Fatalf("router init: %v", err)
	}
	emitter := phonewaveinput.NewEmitter(pub, router)
	watcher := phonewaveinput.NewWatcher(emitter, archive)

	runDone := make(chan error, 1)
	go func() { runDone <- watcher.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)

	bodyMarker := "multi-mode-emitter-marker " + time.Now().Format("150405.000000")
	mail := domain.DMail{
		ID:             "01HZW_MULTI_EMIT",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-multi-emit",
		Body:           bodyMarker,
	}
	path := filepath.Join(archive, "multi.md")
	if err := os.WriteFile(path, []byte(mail.RenderMarkdown()), 0o644); err != nil {
		t.Fatalf("write archive .md: %v", err)
	}

	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	pullCtx, pullCancel := context.WithTimeout(ctx, 15*time.Second)
	defer pullCancel()

	got := make(chan map[string]string, 1)
	sub := subClient.Subscriber(subID)
	go func() {
		_ = sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
			defer m.Ack()
			if strings.Contains(string(m.Data), bodyMarker) {
				attrs := make(map[string]string, len(m.Attributes))
				for k, v := range m.Attributes {
					attrs[k] = v
				}
				select {
				case got <- attrs:
				default:
				}
			}
		})
	}()

	select {
	case attrs := <-got:
		if attrs["project_id"] != "foo" {
			t.Errorf("Pub/Sub message attribute project_id = %q, want foo (full attrs=%v)",
				attrs["project_id"], attrs)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("multi-mode emitter did not publish marker %q within deadline", bodyMarker)
	}

	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("watcher returned unexpected error: %v", err)
	}
}

// TestIntegration_DmailEmitter_MultiModeSkipsUnmappedDir exercises the
// fail-soft branch: a file that lands in a directory the router does not
// know is read but never published, mirroring the receiver-side DLQ but
// without producing any traffic at all (read-only watcher, ADR 0029).
func TestIntegration_DmailEmitter_MultiModeSkipsUnmappedDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicOutbound
	subID := testutils.SubGateway

	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()

	root := t.TempDir()
	archiveFoo := filepath.Join(root, "foo")
	archiveGhost := filepath.Join(root, "ghost") // not registered
	if err := os.MkdirAll(archiveFoo, 0o755); err != nil {
		t.Fatalf("mkdir foo: %v", err)
	}
	if err := os.MkdirAll(archiveGhost, 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}

	router, err := phonewaveinput.NewMultiArchiveRouter(map[string]string{
		"foo": archiveFoo,
	})
	if err != nil {
		t.Fatalf("router init: %v", err)
	}
	emitter := phonewaveinput.NewEmitter(pub, router)
	// Watcher is told about the ghost dir explicitly so it generates an
	// fsnotify event; the router still rejects it.
	watcher := phonewaveinput.NewWatcher(emitter, archiveGhost)

	runDone := make(chan error, 1)
	go func() { runDone <- watcher.Run(ctx) }()
	time.Sleep(200 * time.Millisecond)

	ghostMarker := "ghost-skip-marker " + time.Now().Format("150405.000000")
	mail := domain.DMail{
		ID:             "01HZW_GHOST",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-ghost",
		Body:           ghostMarker,
	}
	path := filepath.Join(archiveGhost, "ghost.md")
	if err := os.WriteFile(path, []byte(mail.RenderMarkdown()), 0o644); err != nil {
		t.Fatalf("write archive .md: %v", err)
	}

	// Drain the topic; we expect *zero* messages with our ghost marker.
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	pullCtx, pullCancel := context.WithTimeout(ctx, 4*time.Second)
	defer pullCancel()

	leak := make(chan struct{}, 1)
	sub := subClient.Subscriber(subID)
	go func() {
		_ = sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
			defer m.Ack()
			if strings.Contains(string(m.Data), ghostMarker) {
				select {
				case leak <- struct{}{}:
				default:
				}
			}
		})
	}()

	select {
	case <-leak:
		t.Fatalf("unmapped path %q leaked into Pub/Sub; emitter must skip", path)
	case <-time.After(3 * time.Second):
		// no leak detected — pass
	}

	cancel()
	if err := <-runDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("watcher returned unexpected error: %v", err)
	}
}
