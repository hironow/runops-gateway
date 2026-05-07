package state

import (
	"context"
	"fmt"
	"os"

	"github.com/hironow/runops-gateway/internal/core/port"
)

// ResolveFromEnv constructs a ProjectRegistry based on RUNOPS_PROJECT_REGISTRY
// (and friends) so cmd/runops and cmd/server share the same opt-in semantics.
//
// Returns (nil, no-op cleanup) when both RUNOPS_PROJECT_REGISTRY and
// RUNOPS_ENV are absent — the registry is treated as opt-in so deployments
// that never touch multiplex stay byte-identical to the pre-#0009 era.
//
// When env asks for a registry but construction fails, the function
// returns a non-nil cleanup (always safe to defer) and a non-nil error.
// Callers may either log + os.Exit (cmd/runops, cmd/server) or surface
// the error to the operator (future HTTP admin endpoint, #0012). The
// shared helper means a future change to the env contract lands in one
// place.
func ResolveFromEnv(ctx context.Context) (port.ProjectRegistry, CleanupFunc, error) {
	if os.Getenv(envProjectRegistry) == "" && os.Getenv(envRunopsEnv) != "development" {
		return nil, noopCleanup, nil
	}
	registry, cleanup, err := NewProjectRegistryFromEnv(ctx, os.Getenv)
	if err != nil {
		return nil, cleanup, fmt.Errorf("resolve project registry: %w", err)
	}
	return registry, cleanup, nil
}
