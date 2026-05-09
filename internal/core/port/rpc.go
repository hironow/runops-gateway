package port

import (
	"context"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

// RPCTransport is the primary port driven by transport adapters (HTTP / WebSocket
// / WebRTC). Per ADR 0040 §JSON-RPC 2.0 spec, every transport adapter consumes
// raw JSON-RPC envelope bytes and produces response bytes via ServeRPC, so the
// adapter is decoupled from method routing and JSON-RPC framing.
//
// Phase 1 has a single concrete transport (HTTP, §B-3); the dispatcher in
// usecase/rpc implements this interface so future WebSocket / WebRTC adapters
// can plug in without changing the dispatcher.
type RPCTransport interface {
	// ServeRPC accepts a single JSON-RPC 2.0 envelope (= raw JSON bytes) and
	// returns the response envelope as bytes. Protocol-level failures
	// (parse / invalid request / method not found / handler error) are
	// returned inside the response envelope (= 200 OK at HTTP layer).
	//
	// A non-nil error indicates a server-internal failure (e.g., response
	// marshal failure) and the caller should map it to the transport's
	// fail-closed status (= HTTP 500).
	ServeRPC(ctx context.Context, raw []byte) ([]byte, error)
}

// OperatorLookup resolves an Authorization Bearer token to an authenticated
// Operator (= human admin operator) via the multi-token admin registry per
// ADR 0040 §identity contract.
//
// The submitted token is opaque to the caller; implementations are expected
// to apply the strict-bearer parsing rules from ADR 0030 §4 before invoking
// this lookup, and to hash (SHA256) the value before consulting their
// internal store.
//
// Lookup MUST be side-effect free; it returns the zero-value Operator and
// false on miss.
type OperatorLookup interface {
	Lookup(submittedToken string) (domainrpc.Operator, bool)
}
