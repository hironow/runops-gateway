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
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	"github.com/hironow/runops-gateway/internal/adapter/observability"
	"github.com/hironow/runops-gateway/internal/adapter/output/phonewave"
)

type config struct {
	projectID    string
	subscription string
	outboxDir    string            // single-mode (PHONEWAVE_OUTBOX_DIR), legacy
	outboxByID   map[string]string // multi-mode (PHONEWAVE_OUTBOX_DIRS_BY_PROJECT), #0006
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

	// Multi-mode env (#0006). Optional; when set it takes precedence
	// over PHONEWAVE_OUTBOX_DIR. The legacy single-mode env stays valid
	// for backward compatibility — at least one of the two must resolve
	// to a non-empty configuration.
	mapEnv := os.Getenv("PHONEWAVE_OUTBOX_DIRS_BY_PROJECT")
	if mapEnv != "" {
		parsed, err := phonewave.ParseOutboxDirsByProject(mapEnv)
		if err != nil {
			return config{}, fmt.Errorf("PHONEWAVE_OUTBOX_DIRS_BY_PROJECT: %w", err)
		}
		if len(parsed) == 0 {
			return config{}, fmt.Errorf("PHONEWAVE_OUTBOX_DIRS_BY_PROJECT parsed to zero entries; either remove the var or supply id:path entries")
		}
		cfg.outboxByID = parsed
	}

	if cfg.outboxDir == "" && len(cfg.outboxByID) == 0 {
		missing = append(missing, "PHONEWAVE_OUTBOX_DIR or PHONEWAVE_OUTBOX_DIRS_BY_PROJECT")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required env vars: %v", missing)
	}
	return cfg, nil
}

// pubsubMessage adapts *pubsub.Message to pubsubinput.Message.
type pubsubMessage struct{ inner *gpubsub.Message }

func (m pubsubMessage) ID() string                    { return m.inner.ID }
func (m pubsubMessage) Data() []byte                  { return m.inner.Data }
func (m pubsubMessage) Attributes() map[string]string { return m.inner.Attributes }
func (m pubsubMessage) Ack()                          { m.inner.Ack() }
func (m pubsubMessage) Nack()                         { m.inner.Nack() }

// otelServiceName returns OTEL_SERVICE_NAME with a sensible default for this
// daemon. ADR 0020: every binary's resource carries service.name.
func otelServiceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return "dmail-receiver"
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("dmail-receiver: configuration error", "error", err)
		os.Exit(1)
	}

	// OpenTelemetry tracing (ADR 0020). Best-effort: failures fall back to
	// no-op so the daemon always boots even if the OTLP endpoint is wrong.
	otelCtx, otelCancel := context.WithTimeout(context.Background(), 10*time.Second)
	tp, err := observability.SetupTracerProvider(otelCtx, observability.Config{
		Endpoint:       os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		ServiceName:    otelServiceName(),
		ServiceVersion: os.Getenv("OTEL_SERVICE_VERSION"),
		Sampler:        os.Getenv("OTEL_TRACES_SAMPLER"),
		SamplerArg:     os.Getenv("OTEL_TRACES_SAMPLER_ARG"),
		GCPProjectID:   os.Getenv("GOOGLE_CLOUD_PROJECT"),
	})
	otelCancel()
	if err != nil {
		slog.Warn("OTel TracerProvider setup failed; telemetry disabled", "error", err)
		tp = sdktrace.NewTracerProvider()
	}
	otel.SetTracerProvider(tp)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			slog.Error("OTel TracerProvider shutdown error", "error", err)
		}
	}()
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

	// Build the OutboxRouter from env: multi-mode takes precedence
	// over single-mode (#0006 / ADR 0028). Setting both is allowed
	// during the transition window — the multi-mode map wins and the
	// legacy single env is logged as deprecated.
	var router pubsubinput.OutboxRouter
	switch {
	case len(cfg.outboxByID) > 0:
		if cfg.outboxDir != "" {
			slog.Warn("dmail-receiver: both PHONEWAVE_OUTBOX_DIRS_BY_PROJECT and PHONEWAVE_OUTBOX_DIR set; map takes precedence (PHONEWAVE_OUTBOX_DIR is deprecated)")
		}
		writers := make(map[string]pubsubinput.Writer, len(cfg.outboxByID))
		for id, dir := range cfg.outboxByID {
			writers[id] = phonewave.NewOutboxWriter(dir)
		}
		router = pubsubinput.NewMultiOutboxRouter(writers)
		slog.Info("dmail-receiver: multi-mode outbox routing", "project_count", len(cfg.outboxByID))
	default:
		writer := phonewave.NewOutboxWriter(cfg.outboxDir)
		router = pubsubinput.NewSingleOutboxRouter(writer)
		slog.Info("dmail-receiver: single-mode outbox (project_id ignored, backward compat)", "outbox_dir", cfg.outboxDir)
	}
	receiver := pubsubinput.NewReceiver(router)

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
