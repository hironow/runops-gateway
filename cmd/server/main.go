package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	gpubsub "cloud.google.com/go/pubsub/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/hironow/runops-gateway/internal/adapter/input/admin"
	"github.com/hironow/runops-gateway/internal/adapter/input/broker"
	pubsubinput "github.com/hironow/runops-gateway/internal/adapter/input/pubsub"
	slackadapter "github.com/hironow/runops-gateway/internal/adapter/input/slack"
	"github.com/hironow/runops-gateway/internal/adapter/observability"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	dispatcheradapter "github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/composition"
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

	// OpenTelemetry tracing (ADR 0020). Setup is best-effort: if it fails, we
	// fall back to a no-op TracerProvider rather than blocking the binary
	// from booting (the "binary always boots" invariant from ADR 0020).
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
	// tp.Shutdown is invoked explicitly at the end of main so it can run
	// **after** pending.Wait — see Issue 0005. A defer here would race the
	// goroutine wait and lose spans whose End() hadn't fired yet.

	// PendingTracker for handler-spawned goroutines (Slack 3-second async
	// pattern, ADR 0002). main waits on this between srv.Shutdown and
	// tp.Shutdown so every span gets End()-ed before the SDK tears down.
	var pending observability.PendingTracker

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

	// Phase 4a (ADR 0019): when DISPATCHER_BACKEND=pubsub, the same publisher
	// also carries approval ack messages back into dmail-inbound. Reuses the
	// existing publisher rather than spinning up a second client.
	approvalPub, err := buildApprovalPublisher(context.Background(), cfg)
	if err != nil {
		slog.Error("failed to build approval publisher", "error", err)
		os.Exit(1)
	}
	if approvalPub != nil {
		defer func() {
			if c, ok := approvalPub.(interface{ Close() error }); ok {
				_ = c.Close()
			}
		}()
	}

	slackHandler := slackadapter.NewInteractiveHandler(svc, dispatchSvc, notifier, consumed, cfg.slackSigningSecret).
		WithPendingTracker(&pending)
	if approvalPub != nil {
		slackHandler = slackHandler.WithApprovalPublisher(approvalPub)
		slog.Info("Phase 4a approval_approve / approval_deny path enabled")
	}
	// Multiplex project registry (#0008/#0009/#0011). Opt-in via env so
	// non-multiplex deployments stay byte-identical. The cleanup is always
	// non-nil; on env error we exit before any handler binds.
	registry, registryCleanup, err := state.ResolveFromEnv(context.Background())
	if err != nil {
		_ = registryCleanup()
		slog.Error("project registry init failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := registryCleanup(); err != nil {
			slog.Error("project registry cleanup failed", "error", err)
		}
	}()

	commandHandler := slackadapter.NewCommandHandler(cfg.slackSigningSecret)
	if registry != nil {
		commandHandler = commandHandler.WithProjectRegistry(registry)
		slog.Info("multiplex project registry wired into Slack /agent (#0008)")
	}

	// Register routes
	mux := http.NewServeMux()
	mux.Handle("POST /slack/interactive", slackHandler)
	mux.Handle("POST /slack/command", commandHandler)

	// Admin endpoint (#0012) is opt-in: registered only when both a
	// project registry and an admin token are configured. Either one
	// missing leaves /admin/projects unreachable so the attack surface
	// stays at zero on non-multiplex deployments. ADR 0030 §"Why opt-in".
	adminToken := os.Getenv("RUNOPS_ADMIN_TOKEN")
	if registry != nil && adminToken != "" {
		admin.NewHandler(registry, adminToken).Register(mux)
		slog.Info("admin endpoint registered (#0012)",
			"endpoints", []string{
				"POST /admin/projects",
				"GET /admin/projects",
				"GET /admin/projects/{id}",
				"POST /admin/projects/{id}/archive",
			})
	} else {
		slog.Info("admin endpoint not registered",
			"registry_wired", registry != nil,
			"admin_token_set", adminToken != "")
	}

	// Token broker endpoint (#0007) is opt-in: registered only when
	// BROKER_AUDIENCE is set AND the project registry is wired.
	// Without one of these the broker stays disabled so dev /
	// staging / pre-rollout deployments behave identically to the
	// pre-broker gateway. See plan v8 §6 step 17 + ADR 0032
	// (caller grant matrix) + ADR 0033 (release-gate).
	//
	// Failure modes (config load / dependency wiring) are logged
	// at ERROR but do NOT abort the binary — the broker is one
	// endpoint among several; the gateway must keep serving Slack
	// + admin even when broker setup is broken. Operators see
	// "broker disabled" in the structured log and can re-deploy
	// after fixing the env / secret.
	if registry != nil && strings.TrimSpace(os.Getenv("BROKER_AUDIENCE")) != "" {
		brokerCfg, err := composition.LoadBrokerConfig()
		if err != nil {
			slog.Error("broker disabled: cannot load config", "err", err)
		} else {
			// When the broker uses the Firestore agent_session_registry
			// for Cloud Run multi-instance safety, the composition root
			// owns the *firestore.Client lifecycle. ProjectID is read
			// from GOOGLE_CLOUD_PROJECT (Cloud Run autopopulates this
			// from the SA project; dev shells set it manually).
			var brokerFirestoreClient *firestore.Client
			if brokerCfg.UseFirestoreRegistry {
				projectID := strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
				if projectID == "" {
					slog.Error("broker disabled: GOOGLE_CLOUD_PROJECT must be set when BROKER_USE_FIRESTORE_REGISTRY=true")
				} else {
					c, err := firestore.NewClient(context.Background(), projectID)
					if err != nil {
						slog.Error("broker disabled: firestore.NewClient", "err", err)
					} else {
						brokerFirestoreClient = c
						defer func() {
							if cerr := c.Close(); cerr != nil {
								slog.Warn("broker firestore client close", "err", cerr)
							}
						}()
					}
				}
			}
			if brokerCfg.UseFirestoreRegistry && brokerFirestoreClient == nil {
				// Fall through with broker disabled — the error has
				// already been logged above.
			} else {
				deps, err := composition.NewBrokerDependencies(context.Background(), brokerCfg, registry, brokerFirestoreClient)
				if err != nil {
					slog.Error("broker disabled: cannot wire dependencies", "err", err)
				} else {
					mux.Handle("POST /broker/token", broker.NewHandler(deps.Service, deps.Authenticator))
					slog.Info("token broker registered (#0007)",
						"endpoints", []string{"POST /broker/token"},
						"use_firestore_registry", brokerCfg.UseFirestoreRegistry,
						"operator_allowlist_size", len(brokerCfg.OperatorEmails),
						"gateway_service_allowlist_size", len(brokerCfg.GatewayServiceSAs),
						"workspace_daemon_allowlist_size", len(brokerCfg.WorkspaceDaemonSAs))
				}
			}
		}
	} else {
		slog.Info("broker endpoint not registered",
			"registry_wired", registry != nil,
			"broker_audience_set", os.Getenv("BROKER_AUDIENCE") != "")
	}
	mux.HandleFunc("GET /_healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// otelhttp wraps the mux so every Slack POST gets a root span automatic.
	// We skip /_healthz to avoid drowning Cloud Trace in liveness probe noise.
	otelMux := otelhttp.NewHandler(mux, "runops-gateway",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/_healthz"
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return r.Method + " " + r.URL.Path
		}),
	)

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.port,
		Handler: otelMux,
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
	// Cloud Run gives 10s between SIGTERM and SIGKILL. Budget [4s, 4s, 2s]:
	//   (1) srv.Shutdown      — drain in-flight HTTP requests
	//   (2) pending.Wait      — wait for handler goroutines to End() spans
	//   (3) tp.Shutdown       — let BSP flush queue + final OTLP export
	// The outbound subscriber cancel runs first; its goroutine has its own
	// pubsub library trace handling and isn't gated on tp.Shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()

	outboundCancel()
	<-outboundDone

	// (1) HTTP graceful: refuse new requests, let in-flight ServeHTTP finish.
	httpCtx, cancelHTTP := context.WithTimeout(shutdownCtx, 4*time.Second)
	defer cancelHTTP()
	if err := srv.Shutdown(httpCtx); err != nil {
		slog.Error("HTTP shutdown error", "error", err)
	}

	// (2) PendingTracker: wait until handler-spawned goroutines End() their
	// spans. Skipping this step is the bug Issue 0005 documents — span
	// flush would race the SDK Shutdown and silently drop traces.
	pending.WaitWithDeadline(shutdownCtx, 4*time.Second)

	// (3) tp.Shutdown: BSP drain + final OTLP export.
	tpCtx, cancelTP := context.WithTimeout(shutdownCtx, 2*time.Second)
	defer cancelTP()
	if err := tp.Shutdown(tpCtx); err != nil {
		slog.Error("OTel TracerProvider shutdown error", "error", err)
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

	// EnableOpenTelemetryTracing per ADR 0021: the library auto-creates
	// receive spans and extracts the W3C context from googclient_* attributes
	// so this subscriber's spans link back to the publisher across the
	// process boundary.
	client, err := gpubsub.NewClientWithConfig(ctx, cfg.pubsubProjectID, &gpubsub.ClientConfig{
		EnableOpenTelemetryTracing: true,
	})
	if err != nil {
		slog.Error("pubsub client for outbound subscriber", "error", err)
		close(done)
		return done
	}
	handler := usecase.NewDispatchResultHandler(notifier)
	// Phase 4a: when the FallbackNotifier and a chat.postMessage URL are
	// available, build an ApprovalRequester so HIGH severity convergence
	// surfaces as a 4-eyes approval prompt instead of a plain reply.
	if cfg.slackBotToken != "" {
		approver := slacknotifier.NewApprovalRequester(slackChatPostMessageURL, cfg.slackBotToken)
		handler = handler.WithApprovalRequester(approver)
		slog.Info("Phase 4a HIGH severity approval requester enabled")
	}
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

// otelServiceName returns OTEL_SERVICE_NAME with a sensible default. Required
// by ADR 0020; the OTel SDK warns when the resource lacks service.name.
func otelServiceName() string {
	if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
		return v
	}
	return "runops-gateway"
}

// buildApprovalPublisher returns a DMailPublisher dedicated to the Phase 4a
// approval ack path, or nil if the deployment is not configured for it. We
// keep it separate from the dispatcher publisher so the lifecycles (and Close
// timing) stay independent — Phase 4a is opt-in.
func buildApprovalPublisher(ctx context.Context, cfg config) (port.DMailPublisher, error) {
	if cfg.dispatcherBackend != "pubsub" {
		return nil, nil
	}
	if cfg.pubsubProjectID == "" || cfg.pubsubInboundTopic == "" {
		return nil, nil
	}
	pub, err := pubsubadapter.NewPublisher(ctx, cfg.pubsubProjectID, cfg.pubsubInboundTopic)
	if err != nil {
		return nil, fmt.Errorf("build approval publisher: %w", err)
	}
	return pub, nil
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
