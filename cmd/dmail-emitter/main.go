// dmail-emitter watches the 5-pillar archive directories on the exe-coder VM
// and publishes any new D-Mail .md it finds to the dmail-outbound Pub/Sub
// topic so the gateway can fan results into Slack threads.
//
// Production deploys it as a systemd unit alongside the receiver. Local
// development runs it against the Firebase Pub/Sub emulator.
//
// Required env vars (one of):
//
//	PHONEWAVE_ARCHIVE_DIRS              — OS-portable list of archive dirs (single-mode, legacy)
//	PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT   — `id1:/abs/foo,id2:/abs/bar` (multi-mode, #0007)
//
// Always required:
//
//	PUBSUB_PROJECT_ID             — GCP project (or "runops-local" for emulator)
//	PUBSUB_DMAIL_OUTBOUND_TOPIC   — Topic to publish onto
//
// Optional:
//
//	PUBSUB_EMULATOR_HOST          — Set to localhost:9399 to use the emulator
//	PHONEWAVE_PEER_RECEIVER_MODE  — "single" or "multi"; when set the
//	                                 emitter refuses to start if its own
//	                                 mode does not match (ADR 0029,
//	                                 codex review v1 #3)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	phonewaveinput "github.com/hironow/runops-gateway/internal/adapter/input/phonewave"
	"github.com/hironow/runops-gateway/internal/adapter/observability"
	"github.com/hironow/runops-gateway/internal/adapter/output/phonewave"
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
	archiveDirs []string          // single-mode (PHONEWAVE_ARCHIVE_DIRS), legacy
	dirsByID    map[string]string // multi-mode (PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT), #0007
	peerMode    string            // optional PHONEWAVE_PEER_RECEIVER_MODE
}

func loadConfig() (config, error) {
	cfg := config{
		projectID: os.Getenv("PUBSUB_PROJECT_ID"),
		topic:     os.Getenv("PUBSUB_DMAIL_OUTBOUND_TOPIC"),
		peerMode:  os.Getenv("PHONEWAVE_PEER_RECEIVER_MODE"),
	}

	// Multi-mode env (#0007). Optional; when set it takes precedence
	// over PHONEWAVE_ARCHIVE_DIRS. The legacy single-mode env stays
	// valid for backward compat — at least one of the two must resolve
	// to a non-empty configuration.
	mapEnv := os.Getenv("PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT")
	if mapEnv != "" {
		parsed, err := phonewave.ParseDirsByProject(mapEnv)
		if err != nil {
			return config{}, fmt.Errorf("PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT: %w", err)
		}
		if len(parsed) == 0 {
			return config{}, fmt.Errorf("PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT parsed to zero entries; either remove the var or supply id:path entries")
		}
		cfg.dirsByID = parsed
	}

	// codex v1 #2: filepath.SplitList honours per-OS list separator
	// (`:` on Linux/macOS, `;` on Windows). Earlier strings.Split(":")
	// would have shredded Windows paths.
	for _, d := range filepath.SplitList(os.Getenv("PHONEWAVE_ARCHIVE_DIRS")) {
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
	if len(cfg.archiveDirs) == 0 && len(cfg.dirsByID) == 0 {
		missing = append(missing, "PHONEWAVE_ARCHIVE_DIRS or PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT")
	}
	if len(missing) > 0 {
		return config{}, fmt.Errorf("missing required env vars: %v", missing)
	}
	switch cfg.peerMode {
	case "", "single", "multi":
	default:
		return config{}, fmt.Errorf("PHONEWAVE_PEER_RECEIVER_MODE: want \"single\" or \"multi\", got %q", cfg.peerMode)
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

	// Build the ArchiveRouter from env: multi-mode takes precedence over
	// single-mode (#0007 / ADR 0029). Both env vars set is allowed during
	// transition — the multi-mode map wins and the legacy single env is
	// noted as deprecated.
	var router phonewaveinput.ArchiveRouter
	var watchedDirs []string
	switch {
	case len(cfg.dirsByID) > 0:
		if len(cfg.archiveDirs) > 0 {
			slog.Warn("dmail-emitter: both PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT and PHONEWAVE_ARCHIVE_DIRS set; map takes precedence (PHONEWAVE_ARCHIVE_DIRS is deprecated)")
		}
		multi, err := phonewaveinput.NewMultiArchiveRouter(cfg.dirsByID)
		if err != nil {
			slog.Error("dmail-emitter: multi router init failed", "error", err)
			os.Exit(1)
		}
		router = multi
		for _, dir := range cfg.dirsByID {
			watchedDirs = append(watchedDirs, dir)
		}
		slog.Info("dmail-emitter: multi-mode archive routing", "project_count", len(cfg.dirsByID))
	default:
		router = phonewaveinput.NewSingleArchiveRouter()
		watchedDirs = cfg.archiveDirs
		slog.Info("dmail-emitter: single-mode archive (project_id from frontmatter only)",
			"archive_count", len(watchedDirs))
	}

	// codex v1 #3: peer-mode guard. When the operator declares the
	// receiver's mode via PHONEWAVE_PEER_RECEIVER_MODE, fail-fast on
	// mismatch so a single-mode emitter cannot quietly publish to a
	// multi-mode receiver (which would NACK every message into the DLQ).
	if cfg.peerMode != "" && cfg.peerMode != router.Mode() {
		slog.Error("emitter mode does not match declared peer receiver mode; refusing to start",
			"emitter_mode", router.Mode(),
			"PHONEWAVE_PEER_RECEIVER_MODE", cfg.peerMode)
		os.Exit(1)
	}
	if cfg.peerMode == "" {
		slog.Warn("PHONEWAVE_PEER_RECEIVER_MODE is unset; emitter cannot verify receiver-side configuration",
			"emitter_mode", router.Mode())
	}

	emitter := phonewaveinput.NewEmitter(pub, router)
	watcher := phonewaveinput.NewWatcher(emitter, watchedDirs...)

	if err := watcher.Run(ctx); err != nil {
		slog.Error("dmail-emitter: watcher exited with error", "error", err)
		os.Exit(1)
	}
	slog.Info("dmail-emitter stopped")
}
