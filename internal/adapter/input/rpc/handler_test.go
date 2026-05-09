package rpc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	inputrpc "github.com/hironow/runops-gateway/internal/adapter/input/rpc"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// Compile-time assertion: *Handler implements http.Handler.
var _ http.Handler = (*inputrpc.Handler)(nil)

// fakeLookup is a minimal port.OperatorLookup for handler tests.
type fakeLookup struct {
	hashToOperator map[string]domainrpc.Operator
}

func newFakeLookup(t *testing.T, entries map[string]string) *fakeLookup {
	t.Helper()
	m := make(map[string]domainrpc.Operator, len(entries))
	for token, operatorID := range entries {
		op, err := domainrpc.NewOperator(operatorID, operatorID+"@example.com")
		if err != nil {
			t.Fatalf("NewOperator: %v", err)
		}
		sum := sha256.Sum256([]byte(token))
		m[hex.EncodeToString(sum[:])] = op
	}
	return &fakeLookup{hashToOperator: m}
}

func (f *fakeLookup) Lookup(submittedToken string) (domainrpc.Operator, bool) {
	sum := sha256.Sum256([]byte(submittedToken))
	op, ok := f.hashToOperator[hex.EncodeToString(sum[:])]
	if !ok {
		return domainrpc.Operator{}, false
	}
	return op, true
}

// echoMethod is a Method that echoes back the operator from context, used
// to assert that the handler propagates identity.
type echoMethod struct {
	captured domainrpc.Operator
	name     string
}

func (e *echoMethod) Name() string { return e.name }
func (e *echoMethod) Handle(ctx context.Context, _ json.RawMessage) (any, *domainrpc.Error) {
	op, ok := usecaserpc.OperatorFromContext(ctx)
	if !ok {
		return nil, &domainrpc.Error{Code: domainrpc.CodeInternalError, Message: "no operator in context"}
	}
	e.captured = op
	return map[string]string{"operator_id": op.OperatorID}, nil
}

func newTestHandler(t *testing.T, lookup port.OperatorLookup) (*inputrpc.Handler, *usecaserpc.Dispatcher) {
	t.Helper()
	disp := usecaserpc.NewDispatcher()
	h := inputrpc.NewHandler(disp, lookup)
	return h, disp
}

// --- transport-layer reject (= 405 / 415 / 401) ---

func TestRPCHandler_NonPOST_Returns405(t *testing.T) {
	// given
	lookup := newFakeLookup(t, nil)
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// when
	resp, err := http.Get(srv.URL + "/rpc")

	// then
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestRPCHandler_WrongContentType_Returns415(t *testing.T) {
	// given
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"a","id":1}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer tok")

	// when
	resp, err := http.DefaultClient.Do(req)

	// then
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusUnsupportedMediaType)
	}
}

func TestRPCHandler_NoBearerHeader_Returns401(t *testing.T) {
	// given
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"a","id":1}`))
	req.Header.Set("Content-Type", "application/json")
	// intentionally no Authorization

	// when
	resp, err := http.DefaultClient.Do(req)

	// then
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestRPCHandler_MalformedBearer_Returns401(t *testing.T) {
	// given - ADR 0030 §4 strict spec: control char in token = reject
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// NOTE: control-char tokens (e.g., "Bearer tok\x00garbage") are pre-rejected
	// by Go's net/http client before the server sees them, so we can't test that
	// path through httptest.NewServer. The control-char path in parseStrictBearer
	// is still defense for raw-socket / proxy edge cases — see the dedicated
	// parseStrictBearer unit test below.
	for _, badHeader := range []string{
		"Bearer",           // too short / no separator
		"Bearer tok extra", // multiple spaces / extra
		"Bearer\ttok",      // tab separator (= ADR 0030: single space only)
		"bearer  tok",      // double space
		"Token tok",        // wrong scheme
	} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"a","id":1}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", badHeader)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do %q: %v", badHeader, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("header %q: status got %d, want %d", badHeader, resp.StatusCode, http.StatusUnauthorized)
		}
	}
}

func TestRPCAuth_TokenRegistryMiss_Returns401(t *testing.T) {
	// given - registry has 'good-token' but request sends 'bad-token'
	lookup := newFakeLookup(t, map[string]string{"good-token": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"a","id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bad-token")

	// when
	resp, err := http.DefaultClient.Do(req)

	// then
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

// --- transport-layer success → JSON-RPC layer (= 200 + envelope) ---

func TestRPCAuth_TokenRegistryHit_ExtractsOperatorID(t *testing.T) {
	// given - registry hit + dispatcher with echo method that captures
	lookup := newFakeLookup(t, map[string]string{"alice-secret": "U_ALICE"})
	disp := usecaserpc.NewDispatcher()
	echo := &echoMethod{name: "echo"}
	disp.Register(echo)
	h := inputrpc.NewHandler(disp, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"echo","id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer alice-secret")

	// when
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// then
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if echo.captured.OperatorID != "U_ALICE" {
		t.Errorf("OperatorID propagated: got %q, want U_ALICE", echo.captured.OperatorID)
	}
	if echo.captured.ActorType != domainrpc.ActorTypeHumanOperator {
		t.Errorf("ActorType: got %q, want %q", echo.captured.ActorType, domainrpc.ActorTypeHumanOperator)
	}
}

func TestRPCHandler_DispatcherUnknownMethod_Returns200WithEnvelope(t *testing.T) {
	// given - empty dispatcher (no methods)
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"unknown","id":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")

	// when
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// then
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var env domainrpc.Response
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error == nil || env.Error.Code != domainrpc.CodeMethodNotFound {
		t.Errorf("expected CodeMethodNotFound, got %+v", env.Error)
	}
}

func TestRPCHandler_DispatcherParseError_Returns200WithEnvelope(t *testing.T) {
	// given
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	h, _ := newTestHandler(t, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")

	// when
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// then
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want %d (parse error returns envelope)", resp.StatusCode, http.StatusOK)
	}
	var env domainrpc.Response
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error == nil || env.Error.Code != domainrpc.CodeParseError {
		t.Errorf("expected CodeParseError, got %+v", env.Error)
	}
}

func TestRPCHandler_ContentTypeWithCharset_Accepted(t *testing.T) {
	// given - "application/json; charset=utf-8" must be accepted (= MIME params)
	lookup := newFakeLookup(t, map[string]string{"tok": "U1"})
	disp := usecaserpc.NewDispatcher()
	disp.Register(&echoMethod{name: "noop"})
	h := inputrpc.NewHandler(disp, lookup)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","method":"noop","id":1}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer tok")

	// when
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	// then - must NOT be 415
	if resp.StatusCode == http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type with charset rejected; expected 200")
	}
}
