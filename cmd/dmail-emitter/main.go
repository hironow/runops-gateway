// dmail-emitter watches the 5-pillar archive directories on the exe-coder VM
// and publishes any new D-Mail .md it finds to the dmail-outbound Pub/Sub
// topic so the gateway can fan results into Slack threads.
//
// Production deploys it as a systemd unit alongside the receiver. Local
// development runs it against the Firebase Pub/Sub emulator.
//
// Required env vars:
//
//	PUBSUB_PROJECT_ID             — GCP project (or "runops-local" for emulator)
//	PUBSUB_DMAIL_OUTBOUND_TOPIC   — Topic to publish onto
//	PHONEWAVE_ARCHIVE_DIRS        — Colon-separated list of archive dirs to watch
//
// Optional:
//
//	PUBSUB_EMULATOR_HOST          — Set to localhost:9399 to use the emulator
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	phonewaveinput "github.com/hironow/runops-gateway/internal/adapter/input/phonewave"
	"github.com/hironow/runops-gateway/internal/adapter/observability"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
)

// otelServiceName returns OTEL_SERVICE_NAME with a sensible default for this
// daemon. ADR 0020: every binary's resource carries service.name.
func otelServiceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return "dmail-emitter"
}

type config struct {
	projectID   string
	topic       string
	archiveDirs []string
}

func loadConfig() (config, error) {
	cfg := config{
		projectID: os.Getenv("PUBSUB_PROJECT_ID"),
		topic:     os.Getenv("PUBSUB_DMAIL_OUTBOUND_TOPIC"),
	}
	dirs := strings.Split(os.Getenv("PHONEWAVE_ARCHIVE_DIRS"), ":")
	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d != "" {
			cfg.archiveDirs = append(cfg.archiveDirs, d)
		}
	}
	missing := []string{}
	if cfg.projectID == "" {
		missing = append(missing, "PUBSUB_PROJECT_ID")
	}
	if cfg.topic == "" {
		missing = append(missing, "PUBSUB_DMAIL_OUTBOUND_TOPIC")
	}
	if len(cfg.archiveDirs) == 0 {
		missing = append(missing, "PHONEWAVE_ARCHIVE_DIRS")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required env vars: %v", missing)
	}
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("dmail-emitter: configuration error", "error", err)
		os.Exit(1)
	}
	slog.Info("dmail-emitter starting",
		"project_id", cfg.projectID,
		"topic", cfg.topic,
		"archive_dirs", cfg.archiveDirs,
		"emulator_host", os.Getenv("PUBSUB_EMULATOR_HOST"),
	)

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

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pub, err := pubsubadapter.NewPublisher(ctx, cfg.projectID, cfg.topic)
	if err != nil {
		slog.Error("dmail-emitter: pubsub publisher", "error", err)
		os.Exit(1)
	}
	defer pub.Close()

	emitter := phonewaveinput.NewEmitter(pub)
	watcher := phonewaveinput.NewWatcher(emitter, cfg.archiveDirs...)

	if err := watcher.Run(ctx); err != nil {
		slog.Error("dmail-emitter: watcher exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("dmail-emitter stopped")
}
