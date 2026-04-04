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

	slackadapter "github.com/hironow/runops-gateway/internal/adapter/input/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/usecase"
)

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

	notifier := slacknotifier.NewResponseURLNotifier()
	authChecker := auth.NewEnvAuthChecker()

	svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())
	slackHandler := slackadapter.NewHandler(svc, cfg.slackSigningSecret)

	// Register routes
	mux := http.NewServeMux()
	mux.Handle("POST /slack/interactive", slackHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})

	// Start server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.port,
		Handler: mux,
	}

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
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("server stopped")
}

type config struct {
	slackSigningSecret string
	port               string
}

func loadConfig() (config, error) {
	cfg := config{
		slackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		port:               os.Getenv("PORT"),
	}
	if cfg.slackSigningSecret == "" {
		return config{}, fmt.Errorf("SLACK_SIGNING_SECRET is required")
	}
	if cfg.port == "" {
		cfg.port = "8080"
	}
	// Log config (never log secrets)
	slog.Info("config loaded",
		"port", cfg.port,
	)
	return cfg, nil
}
