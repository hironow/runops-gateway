package rpc_test

import (
	"encoding/json"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

func TestParseRequest_ValidWithStringID(t *testing.T) {
	// given
	raw := []byte(`{"jsonrpc":"2.0","method":"runops.admin.project.get","params":{"id":"alpha"},"id":"req-1"}`)

	// when
	req, err := rpc.ParseRequest(raw)

	// then
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("JSONRPC: got %q, want %q", req.JSONRPC, "2.0")
	}
	if req.Method != "runops.admin.project.get" {
		t.Errorf("Method: got %q", req.Method)
	}
	if req.IsNotification() {
		t.Errorf("expected non-notification (id present)")
	}
	if string(req.ID) != `"req-1"` {
		t.Errorf("ID raw: got %s, want %q", req.ID, `"req-1"`)
	}
}

func TestParseRequest_ValidWithNumericID(t *testing.T) {
	// given
	raw := []byte(`{"jsonrpc":"2.0","method":"foo","id":42}`)

	// when
	req, err := rpc.ParseRequest(raw)

	// then
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if req.IsNotification() {
		t.Errorf("expected non-notification (numeric id)")
	}
	if string(req.ID) != "42" {
		t.Errorf("ID raw: got %s", req.ID)
	}
}

func TestParseRequest_ValidWithNullID(t *testing.T) {
	// given - id: null is valid per JSON-RPC 2.0 spec, NOT a notification
	raw := []byte(`{"jsonrpc":"2.0","method":"foo","id":null}`)

	// when
	req, err := rpc.ParseRequest(raw)

	// then
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if req.IsNotification() {
		t.Errorf("id:null is NOT a notification (per spec)")
	}
}

func TestParseRequest_NotificationDetected_IDFieldMissing(t *testing.T) {
	// given - id field absent → notification
	raw := []byte(`{"jsonrpc":"2.0","method":"foo"}`)

	// when
	req, err := rpc.ParseRequest(raw)

	// then
	if err != nil {
		t.Fatalf("ParseRequest failed: %v", err)
	}
	if !req.IsNotification() {
		t.Errorf("id field absent → expected notification")
	}
}

func TestParseRequest_RejectsWrongVersion(t *testing.T) {
	// given
	raw := []byte(`{"jsonrpc":"1.0","method":"foo","id":1}`)

	// when
	_, err := rpc.ParseRequest(raw)

	// then
	if err == nil {
		t.Fatalf("expected error for jsonrpc != 2.0")
	}
}

func TestParseRequest_RejectsBatch(t *testing.T) {
	// given - batch (array) not supported in Phase 1 per ADR 0040
	raw := []byte(`[{"jsonrpc":"2.0","method":"foo","id":1}]`)

	// when
	_, err := rpc.ParseRequest(raw)

	// then
	if err == nil {
		t.Fatalf("expected error for batch request")
	}
	if got := rpc.ErrorCode(err); got != rpc.CodeInvalidRequest {
		t.Errorf("error code: got %d, want %d (invalid request)", got, rpc.CodeInvalidRequest)
	}
}

func TestParseRequest_RejectsTrailingJSON(t *testing.T) {
	// given - smuggled second envelope after a valid first one. Without
	// trailing-token detection this would silently parse only the first.
	raw := []byte(`{"jsonrpc":"2.0","method":"a","id":1}{"jsonrpc":"2.0","method":"evil","id":2}`)

	// when
	_, err := rpc.ParseRequest(raw)

	// then
	if err == nil {
		t.Fatalf("expected error for trailing JSON tokens")
	}
	if got := rpc.ErrorCode(err); got != rpc.CodeParseError {
		t.Errorf("error code: got %d, want %d (parse error)", got, rpc.CodeParseError)
	}
}

func TestParseRequest_RejectsInvalidJSON(t *testing.T) {
	// given
	raw := []byte(`{not json`)

	// when
	_, err := rpc.ParseRequest(raw)

	// then
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if got := rpc.ErrorCode(err); got != rpc.CodeParseError {
		t.Errorf("error code: got %d, want %d (parse error)", got, rpc.CodeParseError)
	}
}

func TestParseRequest_RejectsMissingMethod(t *testing.T) {
	// given
	raw := []byte(`{"jsonrpc":"2.0","id":1}`)

	// when
	_, err := rpc.ParseRequest(raw)

	// then
	if err == nil {
		t.Fatalf("expected error for missing method")
	}
	if got := rpc.ErrorCode(err); got != rpc.CodeInvalidRequest {
		t.Errorf("error code: got %d, want %d (invalid request)", got, rpc.CodeInvalidRequest)
	}
}

func TestEncodeResponse_SuccessRoundTrip(t *testing.T) {
	// given
	resp := rpc.Response{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"projects":[]}`),
		ID:      json.RawMessage(`"req-1"`),
	}

	// when
	out, err := rpc.EncodeResponse(resp)

	// then
	if err != nil {
		t.Fatalf("EncodeResponse failed: %v", err)
	}
	var got map[string]any
	if uerr := json.Unmarshal(out, &got); uerr != nil {
		t.Fatalf("re-decode failed: %v", uerr)
	}
	if got["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc: got %v", got["jsonrpc"])
	}
	if _, hasErr := got["error"]; hasErr {
		t.Errorf("success response must NOT contain error field")
	}
	if _, hasResult := got["result"]; !hasResult {
		t.Errorf("success response MUST contain result field")
	}
}

func TestEncodeResponse_ErrorRoundTrip(t *testing.T) {
	// given
	resp := rpc.Response{
		JSONRPC: "2.0",
		Error: &rpc.Error{
			Code:    rpc.CodeMethodNotFound,
			Message: "method not found: foo",
		},
		ID: json.RawMessage(`null`),
	}

	// when
	out, err := rpc.EncodeResponse(resp)

	// then
	if err != nil {
		t.Fatalf("EncodeResponse failed: %v", err)
	}
	var got map[string]any
	if uerr := json.Unmarshal(out, &got); uerr != nil {
		t.Fatalf("re-decode failed: %v", uerr)
	}
	if _, hasResult := got["result"]; hasResult {
		t.Errorf("error response must NOT contain result field")
	}
	gotErr, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("error response missing error object")
	}
	if int(gotErr["code"].(float64)) != rpc.CodeMethodNotFound {
		t.Errorf("error code: got %v, want %d", gotErr["code"], rpc.CodeMethodNotFound)
	}
}
