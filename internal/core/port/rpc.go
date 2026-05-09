package port

import "context"

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
