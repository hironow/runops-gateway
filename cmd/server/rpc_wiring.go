package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/hironow/runops-gateway/internal/adapter/input/rpc"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

// rpcWiringConfig is the input to wireRPCEndpoint.
//
// Decoupled from os.Getenv so tests can drive every state (= flag off,
// registry absent / parse-failed / present, dependency nil) deterministically.
type rpcWiringConfig struct {
	// flagEnabled corresponds to RUNOPS_RPC_ENDPOINT_ENABLED=1.
	flagEnabled bool
	// registryPath corresponds to RUNOPS_ADMIN_TOKENS_REGISTRY_FILE; empty
	// means the env var is not set.
	registryPath string
	// projectRegistry is the read/write registry used by §B-4 read-only
	// methods (= get / list). Must be non-nil when flag on + registryPath set.
	projectRegistry port.ProjectRegistry
	// pendingStore powers `pending.get`. Must be non-nil when flag on +
	// registryPath set.
	pendingStore port.PendingStore
	// highMutationEnabled mirrors RUNOPS_RPC_HIGH_MUTATION_ENABLED=1
	// and gates the §B-5 HIGH severity methods (= project.add / archive).
	// With the flag off the methods are still registered with the
	// dispatcher but return -32000 application error so clients see
	// "feature gated" instead of -32601 "method not found".
	highMutationEnabled bool
	// adminApprovalRequester publishes the convergence D-Mail that
	// surfaces approve / deny buttons in Slack (= §B-5.4b producer
	// side). nil = publishing disabled, mutation methods still record
	// the pending but operators must repost buttons manually.
	adminApprovalRequester port.ApprovalRequester
	// adminApprovalTarget is the Slack channel (and optional thread)
	// where admin approval requests land. Ignored when
	// adminApprovalRequester is nil.
	adminApprovalTarget port.NotifyTarget
}

// wireRPCEndpoint registers POST /rpc on mux when the feature flag is on
// AND a multi-token admin registry is provided AND the §B-4 dependencies
// (ProjectRegistry, PendingStore) are wired.
//
// Per ADR 0040 §identity contract (2026-05-10 user-confirmed scope-out)
// the legacy ADMIN_TOKEN fallback is intentionally not implemented:
// registry absent → endpoint absent (= fail-closed). REST read-only
// remains available behind the existing single-token path.
//
// The returned boolean indicates whether the endpoint was registered.
// A nil error with wired==false means the endpoint was intentionally
// skipped (= flag off, or registry path empty).
//
// §B-4 scope: read-only methods (project.get / list / pending.get) are
// registered. §B-5 will add mutation methods + approval orchestrator.
func wireRPCEndpoint(mux *http.ServeMux, cfg rpcWiringConfig) (bool, error) {
	if !cfg.flagEnabled {
		return false, nil
	}
	if cfg.registryPath == "" {
		// registry なし → fail-closed (= endpoint 不在)
		return false, nil
	}
	if cfg.projectRegistry == nil {
		return false, errors.New("rpc endpoint: projectRegistry must not be nil when flag is enabled")
	}
	if cfg.pendingStore == nil {
		return false, errors.New("rpc endpoint: pendingStore must not be nil when flag is enabled")
	}

	registry, err := auth.LoadAdminTokensRegistry(cfg.registryPath)
	if err != nil {
		return false, fmt.Errorf("rpc endpoint: load admin tokens registry: %w", err)
	}

	dispatcher := usecaserpc.NewDispatcher()
	// §B-4 read-only methods
	dispatcher.Register(methods.NewProjectGet(cfg.projectRegistry))
	dispatcher.Register(methods.NewProjectList(cfg.projectRegistry))
	dispatcher.Register(methods.NewPendingGet(cfg.pendingStore))
	// §B-5.2 HIGH severity mutation methods. Always registered so a
	// client receives -32000 "feature gated" when the flag is off,
	// matching ADR 0040 §approval gate integration step 3.
	addMethod := methods.NewProjectAdd(cfg.pendingStore, cfg.highMutationEnabled)
	archiveMethod := methods.NewProjectArchive(cfg.pendingStore, cfg.highMutationEnabled)
	// §B-5.4b: attach the convergence-D-Mail publisher so a pending
	// surfaces approve / deny buttons in Slack. nil requester is fine
	// (= mutation methods skip publishing but still record pending).
	if cfg.adminApprovalRequester != nil {
		addMethod = addMethod.WithApprovalPublisher(cfg.adminApprovalRequester, cfg.adminApprovalTarget)
		archiveMethod = archiveMethod.WithApprovalPublisher(cfg.adminApprovalRequester, cfg.adminApprovalTarget)
	}
	dispatcher.Register(addMethod)
	dispatcher.Register(archiveMethod)

	handler := rpc.NewHandler(dispatcher, registry)
	mux.Handle("POST /rpc", handler)
	return true, nil
}
