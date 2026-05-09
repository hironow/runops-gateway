package rpc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// fakeMethod is a minimal Method implementation for unit tests.
type fakeMethod struct {
	name    string
	handler func(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error)
}

func (f *fakeMethod) Name() string { return f.name }
func (f *fakeMethod) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	return f.handler(ctx, params)
}

func TestDispatcher_RoutesRegisteredMethod(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	d.Register(&fakeMethod{
		name: "echo",
		handler: func(_ context.Context, params json.RawMessage) (any, *domainrpc.Error) {
			return map[string]any{"echoed": string(params)}, nil
		},
	})
	in := []byte(`{"jsonrpc":"2.0","method":"echo","params":{"x":1},"id":"r1"}`)

	// when
	out, err := d.ServeRPC(context.Background(), in)

	// then
	if err != nil {
		t.Fatalf("ServeRPC failed: %v", err)
	}
	var got domainrpc.Response
	if uerr := json.Unmarshal(out, &got); uerr != nil {
		t.Fatalf("decode: %v", uerr)
	}
	if got.Error != nil {
		t.Fatalf("expected success, got error: %+v", got.Error)
	}
	if !strings.Contains(string(got.Result), `"echoed"`) {
		t.Errorf("Result missing echoed: %s", got.Result)
	}
	if string(got.ID) != `"r1"` {
		t.Errorf("ID: got %s", got.ID)
	}
}

func TestDispatcher_UnknownMethodReturnsMethodNotFound(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	in := []byte(`{"jsonrpc":"2.0","method":"nope","id":1}`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeMethodNotFound {
		t.Errorf("expected CodeMethodNotFound, got %+v", got.Error)
	}
	if string(got.ID) != "1" {
		t.Errorf("ID echo: got %s", got.ID)
	}
}

func TestDispatcher_ParseErrorReturnsParseError(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	in := []byte(`{not json`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeParseError {
		t.Errorf("expected CodeParseError, got %+v", got.Error)
	}
	// id is unknown for parse errors → spec says null
	if string(got.ID) != "null" {
		t.Errorf("parse error must return id: null, got %s", got.ID)
	}
}

func TestDispatcher_NotificationRejected(t *testing.T) {
	// given - id field absent → notification, rejected per ADR 0040 (response required)
	d := usecaserpc.NewDispatcher()
	in := []byte(`{"jsonrpc":"2.0","method":"foo"}`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest for notification, got %+v", got.Error)
	}
}

func TestDispatcher_BatchRejected(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	in := []byte(`[{"jsonrpc":"2.0","method":"foo","id":1}]`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest for batch, got %+v", got.Error)
	}
}

func TestDispatcher_HandlerErrorPropagated(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	d.Register(&fakeMethod{
		name: "fail",
		handler: func(_ context.Context, _ json.RawMessage) (any, *domainrpc.Error) {
			return nil, &domainrpc.Error{Code: domainrpc.CodeApplicationErrorBase, Message: "boom"}
		},
	})
	in := []byte(`{"jsonrpc":"2.0","method":"fail","id":7}`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeApplicationErrorBase {
		t.Errorf("expected handler error to propagate, got %+v", got.Error)
	}
	if string(got.ID) != "7" {
		t.Errorf("ID echo: got %s", got.ID)
	}
}

func TestDispatcher_RegisterDuplicatePanics(t *testing.T) {
	// given
	d := usecaserpc.NewDispatcher()
	d.Register(&fakeMethod{name: "x", handler: nil})

	// when / then - duplicate registration is a programming error
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate Register")
		}
	}()
	d.Register(&fakeMethod{name: "x", handler: nil})
}

func TestDispatcher_HandlerPanicReturnsInternalError(t *testing.T) {
	// given - handler panic must not crash the server; map to -32603 internal error
	d := usecaserpc.NewDispatcher()
	d.Register(&fakeMethod{
		name: "panic",
		handler: func(_ context.Context, _ json.RawMessage) (any, *domainrpc.Error) {
			panic("kaboom")
		},
	})
	in := []byte(`{"jsonrpc":"2.0","method":"panic","id":3}`)

	// when
	out, _ := d.ServeRPC(context.Background(), in)

	// then
	var got domainrpc.Response
	_ = json.Unmarshal(out, &got)
	if got.Error == nil || got.Error.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError on panic, got %+v", got.Error)
	}
}
