// dmail-receiver pulls D-Mail messages from a Pub/Sub subscription
// (dmail-inbound) and atomic-writes them into a phonewave-watched outbox
// directory.
//
// Runs as a long-lived process — production deploys it as a systemd unit on
// the exe-coder VM (see ADR 0013 / 0015). Local development runs it against
// the Firebase Pub/Sub emulator.
//
// Required env vars:
//
//	PUBSUB_PROJECT_ID            — GCP project (or "runops-local" for emulator)
//	PUBSUB_DMAIL_INBOUND_SUB     — Pull subscription on dmail-inbound topic
//	PHONEWAVE_OUTBOX_DIR         — Filesystem dir phonewave is watching
//
// Optional:
//
//	PUBSUB_EMULATOR_HOST         — Set to localhost:9399 to use the emulator
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	gpubsub "cloud.google.com/go/pubsub/v2"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	"github.com/hironow/runops-gateway/internal/adapter/output/phonewave"
)

type config struct {
	projectID    string
	subscription string
	outboxDir    string
}

func loadConfig() (config, error) {
	cfg := config{
		projectID:    os.Getenv("PUBSUB_PROJECT_ID"),
		subscription: os.Getenv("PUBSUB_DMAIL_INBOUND_SUB"),
		outboxDir:    os.Getenv("PHONEWAVE_OUTBOX_DIR"),
	}
	missing := []string{}
	if cfg.projectID == "" {
		missing = append(missing, "PUBSUB_PROJECT_ID")
	}
	if cfg.subscription == "" {
		missing = append(missing, "PUBSUB_DMAIL_INBOUND_SUB")
	}
	if cfg.outboxDir == "" {
		missing = append(missing, "PHONEWAVE_OUTBOX_DIR")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required env vars: %v", missing)
	}
	return cfg, nil
}

// pubsubMessage adapts *pubsub.Message to pubsubinput.Message.
type pubsubMessage struct{ inner *gpubsub.Message }

func (m pubsubMessage) ID() string                      { return m.inner.ID }
func (m pubsubMessage) Data() []byte                    { return m.inner.Data }
func (m pubsubMessage) Attributes() map[string]string   { return m.inner.Attributes }
func (m pubsubMessage) Ack()                            { m.inner.Ack() }
func (m pubsubMessage) Nack()                           { m.inner.Nack() }

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("dmail-receiver: configuration error", "error", err)
		os.Exit(1)
	}
	slog.Info("dmail-receiver starting",
		"project_id", cfg.projectID,
		"subscription", cfg.subscription,
		"outbox_dir", cfg.outboxDir,
		"emulator_host", os.Getenv("PUBSUB_EMULATOR_HOST"),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// EnableOpenTelemetryTracing per ADR 0021: receive spans are stitched to
	// the publisher's trace via googclient_* message attributes that the
	// library auto-extracts.
	client, err := gpubsub.NewClientWithConfig(ctx, cfg.projectID, &gpubsub.ClientConfig{
		EnableOpenTelemetryTracing: true,
	})
	if err != nil {
		slog.Error("dmail-receiver: pubsub client", "error", err)
		os.Exit(1)
	}
	defer client.Close()

	writer := phonewave.NewOutboxWriter(cfg.outboxDir)
	receiver := pubsubinput.NewReceiver(writer)

	sub := client.Subscriber(cfg.subscription)
	slog.Info("dmail-receiver: receiving",
		"subscription", cfg.subscription)
	err = sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
		receiver.OnMessage(ctx, pubsubMessage{inner: m})
	})
	if err != nil && ctx.Err() == nil {
		slog.Error("dmail-receiver: receive loop exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("dmail-receiver stopped")
}
