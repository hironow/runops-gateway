package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hironow/runops-gateway/internal/adapter/input/cli"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	gcpadapter "github.com/hironow/runops-gateway/internal/adapter/output/gcp"
	slacknotifier "github.com/hironow/runops-gateway/internal/adapter/output/slack"
	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/port"
	"github.com/hironow/runops-gateway/internal/usecase"
)

func main() {
	ctx := context.Background()

	gcpCtrl, err := gcpadapter.NewController(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create GCP controller: %v\n", err)
		os.Exit(1)
	}
	defer gcpCtrl.Close()

	notifier := slacknotifier.NewResponseURLNotifier()
	authChecker := auth.NewEnvAuthChecker()
	svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())

	// Project registry (multiplex Phase α #0009). Only constructed when
	// the operator opted in via env. Selection is fail-closed: see
	// state.NewProjectRegistryFromEnv.
	registry := mustResolveProjectRegistry(ctx)

	root := cli.NewRootCmd(svc, registry)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// mustResolveProjectRegistry returns nil when the env never opts in (so the
// CLI keeps working without DB / sqlite for callers that only need approve
// / deny). When env asks for a registry but construction fails, the program
// exits with a clear message — never silently fall back.
func mustResolveProjectRegistry(ctx context.Context) port.ProjectRegistry {
	if os.Getenv("RUNOPS_PROJECT_REGISTRY") == "" && os.Getenv("RUNOPS_ENV") != "development" {
		return nil
	}
	registry, err := state.NewProjectRegistryFromEnv(ctx, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "project registry init: %v\n", err)
		os.Exit(1)
	}
	return registry
}
