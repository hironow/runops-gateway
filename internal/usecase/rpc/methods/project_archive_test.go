package methods_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

func TestProjectArchive_Name(t *testing.T) {
	m := methods.NewProjectArchive(&pendingStoreCapture{}, true)
	if got := m.Name(); got != methods.MethodNameProjectArchive {
		t.Errorf("Name: got %q, want %q", got, methods.MethodNameProjectArchive)
	}
}

func TestProjectArchive_FlagOff_ReturnsApplicationError(t *testing.T) {
	m := methods.NewProjectArchive(&pendingStoreCapture{}, false)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":"alpha"}`))
	if rerr == nil || rerr.Code != domainrpc.CodeApplicationErrorBase {
		t.Errorf("expected CodeApplicationErrorBase, got %+v", rerr)
	}
}

func TestProjectArchive_FlagOn_CreatesPendingArchive(t *testing.T) {
	// given
	store := &pendingStoreCapture{}
	m := methods.NewProjectArchive(store, true)
	params := json.RawMessage(`{"id":"alpha"}`)

	// when
	result, rerr := m.Handle(opCtx(t, "U_ALICE"), params)

	// then
	if rerr != nil {
		t.Fatalf("Handle returned error: %+v", rerr)
	}
	if store.gotPending.Op != domain.PendingOpArchive {
		t.Errorf("Op: got %q, want %q", store.gotPending.Op, domain.PendingOpArchive)
	}
	if store.gotPending.EffectiveRequesterID != "U_ALICE" {
		t.Errorf("EffectiveRequesterID: got %q", store.gotPending.EffectiveRequesterID)
	}
	out, _ := json.Marshal(result)
	var m1 map[string]any
	_ = json.Unmarshal(out, &m1)
	if m1["status"] != "pending_approval" {
		t.Errorf("status: got %v", m1["status"])
	}
}

func TestProjectArchive_EmptyID_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewProjectArchive(&pendingStoreCapture{}, true)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":""}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectArchive_MalformedParams_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewProjectArchive(&pendingStoreCapture{}, true)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{not json`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectArchive_MissingOperator_ReturnsInternal(t *testing.T) {
	m := methods.NewProjectArchive(&pendingStoreCapture{}, true)
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"id":"alpha"}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError, got %+v", rerr)
	}
}

// compile-time assertion
var _ usecaserpc.Method = (*methods.ProjectArchive)(nil)
