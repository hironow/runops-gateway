package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"

	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	slackadapter "github.com/hironow/runops-gateway/internal/adapter/input/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	dispatcheradapter "github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

const slackChatPostMessageURL = "https://slack.com/api/chat.postMessage"

func main() {
	// Load and validate required config
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	// Wire adapters
	gcpCtrl, err := gcpadapter.NewController(context.Background())
	if err != nil {
		slog.Error("failed to create GCP controller", "error", err)
		os.Exit(1)
	}
	defer gcpCtrl.Close()

	primary := slacknotifier.NewResponseURLNotifier()
	// FallbackNotifier (ADR 0017 / Issue 0017) wraps the response_url-based
	// primary so a 30-min expiry or 5-call limit drops into chat.postMessage.
	// Falls back gracefully when SLACK_BOT_TOKEN is unset (primary errors
	// propagate as before — Phase 0 behaviour preserved for deployments that
	// have not provisioned the Bot Token yet).
	var notifier port.Notifier = primary
	if cfg.slackBotToken != "" {
		notifier = slacknotifier.NewFallbackNotifier(primary, slackChatPostMessageURL, cfg.slackBotToken, cfg.slackDefaultChannelID)
		slog.Info("FallbackNotifier enabled", "default_channel_id", cfg.slackDefaultChannelID)
	} else {
		slog.Warn("SLACK_BOT_TOKEN unset — chat.postMessage fallback is DISABLED; long-running operations may lose Slack updates")
	}
	authChecker := auth.NewEnvAuthChecker()

	svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())

	// Phase 1/2a: pick the dispatch backend based on DISPATCHER_BACKEND.
	// Default ("stub") keeps the Phase 1 logging-only behaviour. "pubsub"
	// switches to PubsubDispatcher once PUBSUB_PROJECT_ID and
	// PUBSUB_DMAIL_INBOUND_TOPIC are provisioned (Phase 2a, ADR 0013).
	dispatcher, dispatcherCloser, err := buildDispatcher(context.Background(), cfg)
	if err != nil {
		slog.Error("failed to build dispatcher", "error", err)
		os.Exit(1)
	}
	if dispatcherCloser != nil {
		defer dispatcherCloser()
	}
	dispatchSvc := usecase.NewDispatchService(dispatcher, notifier, authChecker, state.NewMemoryStore())
	// One-time consume guard for dispatch_approve buttons (Codex round 4 #2).
	// 1-hour TTL covers Slack's 30-min response_url window with margin.
	consumed := state.NewMemoryConsumedStore(time.Hour)
	slackHandler := slackadapter.NewInteractiveHandler(svc, dispatchSvc, notifier, consumed, cfg.slackSigningSecret)
	commandHandler := slackadapter.NewCommandHandler(cfg.slackSigningSecret)

	// Register routes
	mux := http.NewServeMux()
	mux.Handle("POST /slack/interactive", slackHandler)
	mux.Handle("POST /slack/command", commandHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.port,
		Handler: mux,
	}

	// Phase 3 (ADR 0018): if PUBSUB_DMAIL_OUTBOUND_SUB is set, start an
	// in-process Pub/Sub StreamingPull that forwards 5-pillar results to a
	// thread reply on the originating Slack message. Empty env keeps the
	// previous (Phase 2a) behaviour where the gateway only publishes.
	outboundCtx, outboundCancel := context.WithCancel(context.Background())
	defer outboundCancel()
	outboundDone := startOutboundSubscriber(outboundCtx, cfg, notifier)

	go func() {
		slog.Info("runops-gateway starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	outboundCancel()
	<-outboundDone
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}

// startOutboundSubscriber starts the dmail-outbound StreamingPull goroutine
// when configured. Returns a channel that closes once the receive loop has
// exited so main can wait for clean shutdown. When the subscription is not
// configured the returned channel is closed immediately.
func startOutboundSubscriber(ctx context.Context, cfg config, notifier port.Notifier) <-chan struct{} {
	done := make(chan struct{})
	if cfg.pubsubOutboundSub == "" {
		slog.Info("dmail-outbound subscriber disabled (PUBSUB_DMAIL_OUTBOUND_SUB unset)")
		close(done)
		return done
	}
	if cfg.pubsubProjectID == "" {
		slog.Warn("PUBSUB_DMAIL_OUTBOUND_SUB set but PUBSUB_PROJECT_ID empty; outbound subscriber disabled")
		close(done)
		return done
	}

	client, err := gpubsub.NewClient(ctx, cfg.pubsubProjectID)
	if err != nil {
		slog.Error("pubsub client for outbound subscriber", "error", err)
		close(done)
		return done
	}
	handler := usecase.NewDispatchResultHandler(notifier)
	receiver := pubsubinput.NewOutboundReceiver(handler)

	go func() {
		defer close(done)
		defer client.Close()
		sub := client.Subscriber(cfg.pubsubOutboundSub)
		slog.Info("dmail-outbound subscriber started",
			"project_id", cfg.pubsubProjectID,
			"subscription", cfg.pubsubOutboundSub)
		err := sub.Receive(ctx, func(ctx context.Context, m *gpubsub.Message) {
			receiver.OnMessage(ctx, outboundMessage{inner: m})
		})
		if err != nil && ctx.Err() == nil {
			slog.Error("dmail-outbound subscriber receive loop exited", "error", err)
		} else {
			slog.Info("dmail-outbound subscriber stopped")
		}
	}()
	return done
}

// outboundMessage adapts *pubsub.Message to pubsubinput.Message.
type outboundMessage struct{ inner *gpubsub.Message }

func (m outboundMessage) ID() string                    { return m.inner.ID }
func (m outboundMessage) Data() []byte                  { return m.inner.Data }
func (m outboundMessage) Attributes() map[string]string { return m.inner.Attributes }
func (m outboundMessage) Ack()                          { m.inner.Ack() }
func (m outboundMessage) Nack()                         { m.inner.Nack() }

type config struct {
	slackSigningSecret    string
	slackBotToken         string // optional — enables FallbackNotifier (ADR 0017)
	slackDefaultChannelID string // optional — only used when target.ChannelID is empty
	port                  string
	dispatcherBackend     string // "stub" (default) or "pubsub" (Phase 2a)
	pubsubProjectID       string // required when backend=pubsub OR outbound sub is set
	pubsubInboundTopic    string // required when backend=pubsub
	pubsubOutboundSub     string // optional — enables Phase 3 OutboundReceiver
}

func loadConfig() (config, error) {
	cfg := config{
		slackSigningSecret:    os.Getenv("SLACK_SIGNING_SECRET"),
		slackBotToken:         os.Getenv("SLACK_BOT_TOKEN"),
		slackDefaultChannelID: os.Getenv("SLACK_DEFAULT_CHANNEL_ID"),
		port:                  os.Getenv("PORT"),
		dispatcherBackend:     os.Getenv("DISPATCHER_BACKEND"),
		pubsubProjectID:       os.Getenv("PUBSUB_PROJECT_ID"),
		pubsubInboundTopic:    os.Getenv("PUBSUB_DMAIL_INBOUND_TOPIC"),
		pubsubOutboundSub:     os.Getenv("PUBSUB_DMAIL_OUTBOUND_SUB"),
	}
	if cfg.slackSigningSecret == "" {
		return config{}, fmt.Errorf("SLACK_SIGNING_SECRET is required")
	}
	if cfg.port == "" {
		cfg.port = "8080"
	}
	if cfg.dispatcherBackend == "" {
		cfg.dispatcherBackend = "stub"
	}
	if cfg.dispatcherBackend == "pubsub" {
		if cfg.pubsubProjectID == "" || cfg.pubsubInboundTopic == "" {
			return config{}, fmt.Errorf("DISPATCHER_BACKEND=pubsub requires PUBSUB_PROJECT_ID and PUBSUB_DMAIL_INBOUND_TOPIC")
		}
	}
	// Log config (never log secrets — bot_token presence is logged as a bool)
	slog.Info("config loaded",
		"port", cfg.port,
		"bot_token_present", cfg.slackBotToken != "",
		"default_channel_id", cfg.slackDefaultChannelID,
		"dispatcher_backend", cfg.dispatcherBackend,
		"pubsub_project_id", cfg.pubsubProjectID,
		"pubsub_inbound_topic", cfg.pubsubInboundTopic,
		"pubsub_outbound_sub", cfg.pubsubOutboundSub,
	)
	return cfg, nil
}

// buildDispatcher returns a port.Dispatcher according to cfg.dispatcherBackend.
// The closer is non-nil only for backends that own a long-lived resource
// (the Pub/Sub publisher); main wires it into a defer so shutdown drains
// in-flight publishes cleanly.
func buildDispatcher(ctx context.Context, cfg config) (port.Dispatcher, func(), error) {
	switch cfg.dispatcherBackend {
	case "stub", "":
		return dispatcheradapter.NewStubDispatcher(slog.Default()), nil, nil
	case "pubsub":
		pub, err := pubsubadapter.NewPublisher(ctx, cfg.pubsubProjectID, cfg.pubsubInboundTopic)
		if err != nil {
			return nil, nil, fmt.Errorf("build pubsub publisher: %w", err)
		}
		return dispatcheradapter.NewPubsubDispatcher(pub), func() {
			if err := pub.Close(); err != nil {
				slog.Error("pubsub publisher close error", "error", err)
			}
		}, nil
	default:
		return nil, nil, fmt.Errorf("unknown DISPATCHER_BACKEND: %q (want stub|pubsub)", cfg.dispatcherBackend)
	}
}
