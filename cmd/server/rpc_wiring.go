package main

import (
	"fmt"
	"net/http"

	"github.com/hironow/runops-gateway/internal/adapter/input/rpc"
	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// rpcWiringConfig is the input to wireRPCEndpoint.
//
// Decoupled from os.Getenv so tests can drive every state (= flag off,
// registry absent / parse-failed / present) deterministically.
type rpcWiringConfig struct {
	// flagEnabled corresponds to RUNOPS_RPC_ENDPOINT_ENABLED=1.
	flagEnabled bool
	// registryPath corresponds to RUNOPS_ADMIN_TOKENS_REGISTRY_FILE; empty
	// means the env var is not set.
	registryPath string
}

// wireRPCEndpoint registers POST /rpc on mux when the feature flag is on
// and a multi-token admin registry has been provided. Per ADR 0040 §B-3
// it is intentionally fail-closed: if the registry file is configured
// but unparseable, an error is returned for the caller to treat as
// startup-fatal. §B-4 will widen the path to allow a legacy single-token
// fallback when the multi-token registry is absent.
//
// The returned boolean indicates whether the endpoint was registered.
// A nil error with wired==false means the endpoint was intentionally
// skipped (= flag off, or registry path empty under §B-3 暫定挙動).
//
// §B-3 scope: the dispatcher is created empty (= no methods registered).
// §B-4 will register read-only project methods; §B-5 will register
// mutation methods plus the admin approval orchestrator.
func wireRPCEndpoint(mux *http.ServeMux, cfg rpcWiringConfig) (bool, error) {
	if !cfg.flagEnabled {
		return false, nil
	}
	if cfg.registryPath == "" {
		// §B-3 暫定挙動: multi-token registry なしでは /rpc は登録しない。
		// §B-4 で legacy `RUNOPS_ADMIN_TOKEN` fallback 経路を追加する想定。
		return false, nil
	}
	registry, err := auth.LoadAdminTokensRegistry(cfg.registryPath)
	if err != nil {
		return false, fmt.Errorf("rpc endpoint: load admin tokens registry: %w", err)
	}
	dispatcher := usecaserpc.NewDispatcher() // §B-4 / §B-5 で method を register
	handler := rpc.NewHandler(dispatcher, registry)
	mux.Handle("POST /rpc", handler)
	return true, nil
}
