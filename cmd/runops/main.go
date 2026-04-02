package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/usecase"
)

func main() {
	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		fmt.Fprintln(os.Stderr, "error: GOOGLE_CLOUD_PROJECT environment variable is required")
		os.Exit(1)
	}
	location := os.Getenv("CLOUD_RUN_LOCATION")
	if location == "" {
		location = "asia-northeast1"
	}

	gcpCtrl, err := gcpadapter.NewController(gcpadapter.Config{
		ProjectID: projectID,
		Location:  location,
	})
	if err != nil {
		slog.Error("failed to create GCP controller", "error", err)
		os.Exit(1)
	}

	notifier := slacknotifier.NewResponseURLNotifier()
	authChecker := auth.NewEnvAuthChecker()
	svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker)

	root := cli.NewRootCmd(svc)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
