// Package rpc defines JSON-RPC 2.0 envelope types and codec helpers.
//
// Per ADR 0040 §JSON-RPC 2.0 spec 準拠:
//   - jsonrpc field MUST be "2.0"
//   - id field is required for non-notification requests; absent id = notification
//   - id: null is valid (NOT a notification, response required)
//   - batch (= JSON array) is unsupported in Phase 1 (= rejected as invalid request)
//   - notification is rejected at admin endpoint (= response required for mutation)
//
// This package contains only domain types + pure encode/decode helpers.
// Routing (Dispatcher) lives in internal/usecase/rpc.
package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// JSONRPCVersion is the only accepted version per ADR 0040.
const JSONRPCVersion = "2.0"

// JSON-RPC 2.0 spec error codes.
//
// Reserved range -32768 〜 -32000 per spec; application errors use -32000 〜 -32099.
const (
	// CodeParseError = invalid JSON received.
	CodeParseError = -32700
	// CodeInvalidRequest = the JSON sent is not a valid Request object.
	CodeInvalidRequest = -32600
	// CodeMethodNotFound = the method does not exist / is not available.
	CodeMethodNotFound = -32601
	// CodeInvalidParams = invalid method parameter(s).
	CodeInvalidParams = -32602
	// CodeInternalError = internal JSON-RPC error.
	CodeInternalError = -32603

	// CodeApplicationErrorBase is the start of the application-defined error range.
	// Application errors should use codes in [-32099, -32000].
	CodeApplicationErrorBase = -32000
)

// Request is a parsed JSON-RPC 2.0 Request object.
//
// ID is stored as raw JSON to distinguish:
//   - field absent (= notification, len(ID) == 0)
//   - field present as null (= valid request, ID == "null")
//   - field present as string/number (= valid request)
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// ID is raw JSON; len(ID) == 0 means the field was absent (= notification).
	// json.RawMessage of "null" means id was explicitly null (NOT a notification).
	ID json.RawMessage `json:"id,omitempty"`
}

// IsNotification reports whether the request is a notification per JSON-RPC 2.0.
//
// Per spec: a Notification is a Request object without an "id" member.
// Explicit `id: null` is a valid Request (not a notification).
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0
}

// Response is a JSON-RPC 2.0 Response object.
//
// Either Result or Error MUST be present; never both.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

// Error is a JSON-RPC 2.0 Error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements the error interface so an *Error may be returned where
// a Go error is expected (= ParseRequest returns *Error wrapped via parseError).
func (e *Error) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// ErrorCode extracts the JSON-RPC error code from an error.
// Returns 0 if err is nil or does not wrap an *Error.
func ErrorCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// ParseRequest parses raw bytes into a Request.
//
// Returns an *Error wrapped as error so the caller (= Dispatcher) can map
// to the appropriate JSON-RPC response code:
//   - CodeParseError (-32700) for invalid JSON / batch
//   - CodeInvalidRequest (-32600) for spec violations (wrong version, missing method)
func ParseRequest(raw []byte) (*Request, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, &Error{Code: CodeInvalidRequest, Message: "empty request body"}
	}

	// Reject batch (= JSON array) per ADR 0040 §JSON-RPC 2.0 spec: Phase 1 unsupported.
	first := bytes.TrimLeft(raw, " \t\r\n")
	if len(first) > 0 && first[0] == '[' {
		return nil, &Error{Code: CodeInvalidRequest, Message: "batch requests are not supported"}
	}

	// Decode into a partial form to detect id field presence vs null.
	var probe struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  *string         `json:"method"`
		Params  json.RawMessage `json:"params"`
		ID      json.RawMessage `json:"id"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&probe); err != nil {
		return nil, &Error{Code: CodeParseError, Message: "invalid JSON"}
	}
	// Reject trailing tokens (= prevent "{valid}{garbage}" smuggling). The
	// decoder is a streaming parser; a second Decode that does NOT return
	// io.EOF means there is more content beyond the first envelope.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, &Error{Code: CodeParseError, Message: "trailing data after JSON-RPC envelope"}
	}

	if probe.JSONRPC != JSONRPCVersion {
		return nil, &Error{Code: CodeInvalidRequest, Message: "jsonrpc must be 2.0"}
	}
	if probe.Method == nil || *probe.Method == "" {
		return nil, &Error{Code: CodeInvalidRequest, Message: "method is required"}
	}

	return &Request{
		JSONRPC: probe.JSONRPC,
		Method:  *probe.Method,
		Params:  probe.Params,
		ID:      probe.ID,
	}, nil
}

// EncodeResponse serializes a Response to JSON bytes.
//
// Enforces the spec invariant: exactly one of Result/Error is set.
func EncodeResponse(resp Response) ([]byte, error) {
	if resp.JSONRPC == "" {
		resp.JSONRPC = JSONRPCVersion
	}
	if (len(resp.Result) > 0) == (resp.Error != nil) {
		return nil, errors.New("response must have exactly one of result or error")
	}
	if len(resp.ID) == 0 {
		resp.ID = json.RawMessage("null")
	}
	return json.Marshal(resp)
}

// NewErrorResponse builds an error response envelope.
// id may be empty (will be encoded as null per spec for parse errors).
func NewErrorResponse(id json.RawMessage, code int, message string, data any) Response {
	return Response{
		JSONRPC: JSONRPCVersion,
		Error:   &Error{Code: code, Message: message, Data: data},
		ID:      id,
	}
}
