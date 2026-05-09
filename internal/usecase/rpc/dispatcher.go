// Package rpc implements the JSON-RPC 2.0 dispatcher.
//
// Per ADR 0040 §JSON-RPC 2.0 spec 準拠 / §method 命名規約:
//   - Dispatcher routes incoming JSON-RPC requests to registered Method handlers.
//   - It is transport-agnostic: HTTP / WebSocket / WebRTC adapters all delegate
//     to ServeRPC(ctx, raw) → response bytes.
//   - Notifications are rejected (= CodeInvalidRequest); admin endpoint requires
//     a response for every mutation.
//   - Batch requests are rejected (= Phase 1, see ADR 0040 §JSON-RPC 2.0 spec).
//
// This package only handles the routing surface. Method-specific business logic
// lives in higher-level usecases (= §B-4 / §B-5 register Method instances here).
package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

// Method is a single JSON-RPC method handler.
//
// Name() must be unique across the Dispatcher; duplicate registration panics
// (= programming error).
type Method interface {
	// Name returns the JSON-RPC method name (e.g., "runops.admin.project.add").
	Name() string
	// Handle executes the method with raw JSON params and returns either a
	// result (= JSON-marshalable) or an *Error envelope.
	Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error)
}

// Dispatcher routes JSON-RPC requests to registered Method handlers.
type Dispatcher struct {
	methods map[string]Method
}

// NewDispatcher returns an empty Dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{methods: make(map[string]Method)}
}

// Register adds a Method to the Dispatcher. Panics if the method name is
// already registered (= programming error, surfaced at startup).
func (d *Dispatcher) Register(m Method) {
	if _, exists := d.methods[m.Name()]; exists {
		panic(fmt.Sprintf("rpc: method %q already registered", m.Name()))
	}
	d.methods[m.Name()] = m
}

// ServeRPC parses raw JSON-RPC bytes, routes to the registered Method, and
// encodes the response.
//
// Per ADR 0040 §Enforcement inventory:
//   - parse error → CodeParseError (-32700) with id: null
//   - invalid request (= wrong version, missing method, batch, notification)
//     → CodeInvalidRequest (-32600)
//   - unknown method → CodeMethodNotFound (-32601)
//   - handler panic → CodeInternalError (-32603) (= server stays alive)
//
// The returned error is always nil for protocol-level failures (those are
// encoded in the response envelope). A non-nil error is returned only when
// the response itself cannot be marshaled, which is a server bug.
func (d *Dispatcher) ServeRPC(ctx context.Context, raw []byte) ([]byte, error) {
	req, perr := domainrpc.ParseRequest(raw)
	if perr != nil {
		// Parse / invalid-request errors: id is unknown → null per spec.
		return domainrpc.EncodeResponse(domainrpc.NewErrorResponse(
			nil,
			domainrpc.ErrorCode(perr),
			perr.Error(),
			nil,
		))
	}
	if req.IsNotification() {
		// ADR 0040: notifications are not accepted at admin endpoint.
		return domainrpc.EncodeResponse(domainrpc.NewErrorResponse(
			nil,
			domainrpc.CodeInvalidRequest,
			"notifications are not supported",
			nil,
		))
	}

	method, ok := d.methods[req.Method]
	if !ok {
		return domainrpc.EncodeResponse(domainrpc.NewErrorResponse(
			req.ID,
			domainrpc.CodeMethodNotFound,
			fmt.Sprintf("method not found: %s", req.Method),
			nil,
		))
	}

	result, herr := d.invoke(ctx, method, req.Params)
	if herr != nil {
		return domainrpc.EncodeResponse(domainrpc.Response{
			Error: herr,
			ID:    req.ID,
		})
	}

	resultBytes, mErr := json.Marshal(result)
	if mErr != nil {
		return domainrpc.EncodeResponse(domainrpc.NewErrorResponse(
			req.ID,
			domainrpc.CodeInternalError,
			fmt.Sprintf("result marshal failed: %v", mErr),
			nil,
		))
	}
	return domainrpc.EncodeResponse(domainrpc.Response{
		Result: resultBytes,
		ID:     req.ID,
	})
}

// invoke calls the method handler with panic recovery so a buggy handler
// cannot crash the server (= maps to CodeInternalError).
func (d *Dispatcher) invoke(ctx context.Context, m Method, params json.RawMessage) (result any, err *domainrpc.Error) {
	defer func() {
		if r := recover(); r != nil {
			err = &domainrpc.Error{
				Code:    domainrpc.CodeInternalError,
				Message: fmt.Sprintf("handler panic: %v", r),
			}
		}
	}()
	return m.Handle(ctx, params)
}
