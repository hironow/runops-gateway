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

	// Project registry (multiplex Phase α #0009/#0011). Only constructed
	// when the operator opted in via env. Selection is fail-closed: see
	// state.NewProjectRegistryFromEnv. cleanup is non-nil even on the
	// nil-registry path so deferring is always safe.
	registry, cleanup := mustResolveProjectRegistry(ctx)
	defer func() {
		if err := cleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "project registry cleanup: %v\n", err)
		}
	}()

	root := cli.NewRootCmd(svc, registry)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// mustResolveProjectRegistry delegates to state.ResolveFromEnv so the same
// fail-closed semantics apply identically to cmd/runops and cmd/server
// (#0008). On error we exit with a clear message; we never silently fall
// back. The cleanup is always non-nil so callers can defer it
// unconditionally.
func mustResolveProjectRegistry(ctx context.Context) (port.ProjectRegistry, state.CleanupFunc) {
	registry, cleanup, err := state.ResolveFromEnv(ctx)
	if err != nil {
		_ = cleanup()
		fmt.Fprintf(os.Stderr, "project registry init: %v\n", err)
		os.Exit(1)
	}
	return registry, cleanup
}
