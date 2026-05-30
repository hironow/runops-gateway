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
	testutils "github.com/hironow/runops-gateway/tests/utils"
)

func TestIntegration_DispatchPublish_ReceivedAndWrittenToOutbox(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	// 1. Spin up the receiver against a temp outbox dir.
	outboxDir := t.TempDir()
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	writer := phonewave.NewOutboxWriter(outboxDir)
	receiver := pubsubinput.NewReceiver(pubsubinput.NewSingleOutboxRouter(writer))

	// Run the receive loop in the background — cancel via ctx when we are done.
	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, testutils.MsgAdapter{Inner: m})
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

// waitForOutboxFileWithBody polls dir until a file whose contents include
// marker shows up, returning its name. Used by the multi-mode tests to
// distinguish whether a message landed in projA's or projB's outbox.
func waitForOutboxFileWithBody(t *testing.T, dir, marker string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			if strings.Contains(string(data), marker) {
				return e.Name()
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return ""
}

// outboxIsEmpty reports whether dir contains zero non-directory entries.
// Multi-mode tests assert that messages addressed to one project never
// leak into another project's outbox.
func outboxIsEmpty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read outbox %q: %v", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			return false
		}
	}
	return true
}

// publishWithProjectID publishes a one-off DMail with the given project_id
// attribute and body marker so the receiver can be observed routing it.
// Returns the body string used as the outbox-file marker.
func publishWithProjectID(t *testing.T, ctx context.Context, projectID, topicID, project, marker string) {
	t.Helper()
	pub, err := pubsubadapter.NewPublisher(ctx, projectID, topicID)
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	defer pub.Close()
	d := dispatcher.NewPubsubDispatcher(pub)

	stamp := time.Now().Format("150405.000000")
	req := domain.DispatchRequest{
		Role:           domain.AgentRolePaintress,
		Text:           marker + " " + stamp,
		RequesterID:    "U_INT_MULTI",
		IdempotencyKey: "multi-" + stamp,
		IssuedAt:       time.Now().Unix(),
		ProjectID:      project,
	}
	if err := d.Dispatch(ctx, req); err != nil {
		t.Fatalf("Dispatch(%q): %v", project, err)
	}
}

// TestIntegration_Receiver_MultiModeRoutesByProjectID covers the happy
// path for multi-mode routing: two messages with distinct project_id
// attributes land in their respective outboxes (#0006).
func TestIntegration_Receiver_MultiModeRoutesByProjectID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	dirA := t.TempDir()
	dirB := t.TempDir()
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	router := pubsubinput.NewMultiOutboxRouter(map[string]pubsubinput.Writer{
		"foo": phonewave.NewOutboxWriter(dirA),
		"bar": phonewave.NewOutboxWriter(dirB),
	})
	receiver := pubsubinput.NewReceiver(router)

	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, testutils.MsgAdapter{Inner: m})
		})
	}()

	markerFoo := "multi-mode-foo-marker"
	markerBar := "multi-mode-bar-marker"
	publishWithProjectID(t, ctx, projectID, topicID, "foo", markerFoo)
	publishWithProjectID(t, ctx, projectID, topicID, "bar", markerBar)

	if got := waitForOutboxFileWithBody(t, dirA, markerFoo, 15*time.Second); got == "" {
		t.Errorf("foo marker did not land in dirA; dir=%s", dirA)
	}
	if got := waitForOutboxFileWithBody(t, dirB, markerBar, 15*time.Second); got == "" {
		t.Errorf("bar marker did not land in dirB; dir=%s", dirB)
	}

	// Cross-leak assertions: foo's marker must not appear in bar's outbox
	// and vice versa.
	if name := waitForOutboxFileWithBody(t, dirA, markerBar, 1*time.Second); name != "" {
		t.Errorf("bar marker leaked into dirA as %q", name)
	}
	if name := waitForOutboxFileWithBody(t, dirB, markerFoo, 1*time.Second); name != "" {
		t.Errorf("foo marker leaked into dirB as %q", name)
	}

	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop unexpected error: %v", err)
	}
}

// TestIntegration_Receiver_MultiModeNacksUnknownProjectID covers the
// fail-closed branch: a message whose project_id is not in the router
// gets nacked, so neither outbox writes anything (#0006 / ADR 0028).
func TestIntegration_Receiver_MultiModeNacksUnknownProjectID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	dirA := t.TempDir()
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	router := pubsubinput.NewMultiOutboxRouter(map[string]pubsubinput.Writer{
		"foo": phonewave.NewOutboxWriter(dirA),
	})
	receiver := pubsubinput.NewReceiver(router)

	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, testutils.MsgAdapter{Inner: m})
		})
	}()

	markerGhost := "multi-mode-ghost-marker"
	publishWithProjectID(t, ctx, projectID, topicID, "ghost", markerGhost)

	// Wait long enough for the message to be redelivered a few times by
	// the emulator (which honours nack), then assert no outbox writes.
	time.Sleep(3 * time.Second)
	if !outboxIsEmpty(t, dirA) {
		t.Errorf("dirA should be empty when project_id is unmapped; got entries")
	}

	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop unexpected error: %v", err)
	}
}

// TestIntegration_Receiver_SingleModeIgnoresProjectIDAttribute covers the
// backward-compat branch: when wired with SingleOutboxRouter, the
// receiver writes to its sole outbox even if project_id is set on the
// inbound message (#0006 / ADR 0028).
func TestIntegration_Receiver_SingleModeIgnoresProjectIDAttribute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	projectID := testutils.FirebaseProjectID
	topicID := testutils.TopicInbound
	subID := testutils.SubReceiver

	dir := t.TempDir()
	subClient, err := gpubsub.NewClient(ctx, projectID)
	if err != nil {
		t.Fatalf("subscriber client: %v", err)
	}
	defer subClient.Close()

	receiver := pubsubinput.NewReceiver(
		pubsubinput.NewSingleOutboxRouter(phonewave.NewOutboxWriter(dir)),
	)

	receiveDone := make(chan error, 1)
	go func() {
		sub := subClient.Subscriber(subID)
		receiveDone <- sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, testutils.MsgAdapter{Inner: m})
		})
	}()

	marker := "single-mode-with-project-id-marker"
	// project_id is set, but single-mode should ignore it and still write
	// to the lone outbox.
	publishWithProjectID(t, ctx, projectID, topicID, "any-id-ignored", marker)

	if got := waitForOutboxFileWithBody(t, dir, marker, 15*time.Second); got == "" {
		t.Errorf("single-mode receiver did not write to outbox; dir=%s", dir)
	}

	cancel()
	if err := <-receiveDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("receive loop unexpected error: %v", err)
	}
}
