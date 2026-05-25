package main

import (
	"encoding/json"
	"sort"

	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

// rpcReadiness captures the wiring state that the /_healthz endpoint
// exposes to operators after the ADR 0040 §B-5 admin-mutation flow
// lands. Each field is set at server startup and read at probe time;
// the struct is immutable after construction.
//
// The shape is stable so operators can grep the response (= no field
// renames without a corresponding rollout doc update).
type rpcReadiness struct {
	// EndpointEnabled mirrors RUNOPS_RPC_ENDPOINT_ENABLED and reports
	// whether the /rpc handler was wired into the mux.
	EndpointEnabled bool `json:"endpoint_enabled"`
	// HighMutationEnabled mirrors RUNOPS_RPC_HIGH_MUTATION_ENABLED and
	// reports whether project.add / archive accept requests (= true)
	// or return -32000 "feature gated" (= false).
	HighMutationEnabled bool `json:"high_mutation_enabled"`
	// RegistryLoaded reports whether the multi-token admin registry
	// was successfully parsed. False means /rpc is not registered.
	RegistryLoaded bool `json:"registry_loaded"`
	// AdminApprovalProducerEnabled reports whether the convergence
	// D-Mail publisher is wired (= mutation methods can surface
	// approve / deny buttons in Slack).
	AdminApprovalProducerEnabled bool `json:"admin_approval_producer_enabled"`
	// AdminApprovalConsumerEnabled reports whether the Slack handler
	// routes approval-ack to the orchestrator (= clicks apply the
	// mutation to the project registry).
	AdminApprovalConsumerEnabled bool `json:"admin_approval_consumer_enabled"`
	// RegisteredMethods enumerates the JSON-RPC methods grouped by
	// severity. Stable lexical ordering inside each group.
	RegisteredMethods rpcReadinessMethods `json:"registered_methods"`
}

type rpcReadinessMethods struct {
	High []string `json:"high"`
	Low  []string `json:"low"`
}

// newRPCReadiness assembles the readiness snapshot from the wiring
// state. The method enumerations are sourced from the severity
// classifier so a future method addition is reflected automatically
// (= ListBySeverity guarantees the regression-test coverage).
func newRPCReadiness(
	endpointEnabled, highMutationEnabled, registryLoaded bool,
	producerEnabled, consumerEnabled bool,
) rpcReadiness {
	high := methods.ListBySeverity(methods.SeverityHigh)
	low := methods.ListBySeverity(methods.SeverityLow)
	sort.Strings(high)
	sort.Strings(low)
	return rpcReadiness{
		EndpointEnabled:              endpointEnabled,
		HighMutationEnabled:          highMutationEnabled,
		RegistryLoaded:               registryLoaded,
		AdminApprovalProducerEnabled: producerEnabled,
		AdminApprovalConsumerEnabled: consumerEnabled,
		RegisteredMethods: rpcReadinessMethods{
			High: high,
			Low:  low,
		},
	}
}

// readinessPayload is the body returned by /_healthz. The top-level
// status field stays "ok" for backwards compatibility with existing
// load balancers; ADR0040 carries the §B-5 wiring snapshot.
type readinessPayload struct {
	Status  string       `json:"status"`
	ADR0040 rpcReadiness `json:"adr_0040"`
}

// marshalReadiness builds the /_healthz response body. Centralised so
// the JSON shape is testable without spinning up a full server.
func marshalReadiness(r rpcReadiness) ([]byte, error) {
	return json.Marshal(readinessPayload{Status: "ok", ADR0040: r})
}
