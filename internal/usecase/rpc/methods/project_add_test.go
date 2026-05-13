package methods_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

// pendingStoreCapture records CreateIfNotExists calls for assertion.
type pendingStoreCapture struct {
	gotPending domain.PendingApproval
	calls      int
	err        error
	existing   *domain.PendingApproval
}

func (s *pendingStoreCapture) CreateIfNotExists(_ context.Context, p domain.PendingApproval) (domain.PendingApproval, error) {
	s.calls++
	s.gotPending = p
	if s.err != nil {
		return domain.PendingApproval{}, s.err
	}
	if s.existing != nil {
		return *s.existing, port.ErrPendingAlreadyExists
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.Status == "" {
		p.Status = domain.PendingStatusPendingApproval
	}
	return p, nil
}
func (s *pendingStoreCapture) Get(_ context.Context, _ string) (domain.PendingApproval, error) {
	return domain.PendingApproval{}, errors.New("not used")
}
func (s *pendingStoreCapture) Transition(_ context.Context, _ string, _ domain.PendingStatus, _ *time.Time) error {
	return errors.New("not used")
}

func opCtx(t *testing.T, operatorID string) context.Context {
	t.Helper()
	op, err := domainrpc.NewOperator(operatorID, operatorID+"@example.com")
	if err != nil {
		t.Fatalf("NewOperator: %v", err)
	}
	return usecaserpc.WithOperator(context.Background(), op)
}

func TestProjectAdd_Name(t *testing.T) {
	m := methods.NewProjectAdd(&pendingStoreCapture{}, true)
	if got := m.Name(); got != methods.MethodNameProjectAdd {
		t.Errorf("Name: got %q, want %q", got, methods.MethodNameProjectAdd)
	}
}

func TestProjectAdd_FlagOff_ReturnsApplicationError(t *testing.T) {
	// given - flag disabled
	m := methods.NewProjectAdd(&pendingStoreCapture{}, false)

	// when
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":"alpha"}`))

	// then - -32000 application-defined error per ADR 0040
	if rerr == nil || rerr.Code != domainrpc.CodeApplicationErrorBase {
		t.Errorf("expected CodeApplicationErrorBase (-32000), got %+v", rerr)
	}
}

func TestProjectAdd_FlagOff_DoesNotCreatePending(t *testing.T) {
	// given
	store := &pendingStoreCapture{}
	m := methods.NewProjectAdd(store, false)

	// when
	_, _ = m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":"alpha"}`))

	// then - no pending state written when feature flag off
	if store.calls != 0 {
		t.Errorf("CreateIfNotExists called %d times when flag off; want 0", store.calls)
	}
}

func TestProjectAdd_FlagOn_CreatesPendingApproval(t *testing.T) {
	// given
	store := &pendingStoreCapture{}
	m := methods.NewProjectAdd(store, true)
	params := json.RawMessage(`{"id":"alpha","github_org":"acme","github_repo":"alpha-repo","workspace_path":"/srv/projects/alpha"}`)

	// when
	result, rerr := m.Handle(opCtx(t, "U_ALICE"), params)

	// then
	if rerr != nil {
		t.Fatalf("Handle returned error: %+v", rerr)
	}
	if store.calls != 1 {
		t.Errorf("CreateIfNotExists calls: got %d, want 1", store.calls)
	}
	if store.gotPending.Op != domain.PendingOpAdd {
		t.Errorf("Op: got %q, want %q", store.gotPending.Op, domain.PendingOpAdd)
	}
	if store.gotPending.EffectiveRequesterID != "U_ALICE" {
		t.Errorf("EffectiveRequesterID: got %q, want U_ALICE", store.gotPending.EffectiveRequesterID)
	}
	if store.gotPending.RequesterActorType != string(domainrpc.ActorTypeHumanOperator) {
		t.Errorf("RequesterActorType: got %q", store.gotPending.RequesterActorType)
	}
	if len(store.gotPending.BodyJSON) == 0 {
		t.Errorf("BodyJSON must not be empty")
	}
	if store.gotPending.IdempotencyKey == "" {
		t.Errorf("IdempotencyKey must be set")
	}
	// verify result envelope shape
	out, _ := json.Marshal(result)
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if got["status"] != "pending_approval" {
		t.Errorf("result.status: got %v, want pending_approval", got["status"])
	}
	if _, ok := got["idempotency_key"].(string); !ok {
		t.Errorf("result.idempotency_key missing: %+v", got)
	}
}

func TestProjectAdd_IdempotentRetry_ReturnsSameKey(t *testing.T) {
	// given - same operator + params submitted twice
	store := &pendingStoreCapture{}
	m := methods.NewProjectAdd(store, true)
	params := json.RawMessage(`{"id":"alpha","github_org":"acme","github_repo":"alpha-repo","workspace_path":"/srv/projects/alpha"}`)

	// when - first call
	r1, _ := m.Handle(opCtx(t, "U_ALICE"), params)
	out1, _ := json.Marshal(r1)
	var m1 map[string]any
	_ = json.Unmarshal(out1, &m1)
	key1 := m1["idempotency_key"].(string)

	// simulate existing record on second call (= duplicate detected)
	existingCopy := store.gotPending
	store.existing = &existingCopy

	// second call with same params
	r2, _ := m.Handle(opCtx(t, "U_ALICE"), params)
	out2, _ := json.Marshal(r2)
	var m2 map[string]any
	_ = json.Unmarshal(out2, &m2)
	key2 := m2["idempotency_key"].(string)

	// then
	if key1 != key2 {
		t.Errorf("idempotent retry key mismatch: %q vs %q", key1, key2)
	}
}

func TestProjectAdd_MissingOperator_ReturnsInternal(t *testing.T) {
	// given - context without operator (= shouldn't happen via §B-3 auth,
	// but defensive)
	m := methods.NewProjectAdd(&pendingStoreCapture{}, true)

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"id":"alpha"}`))

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError, got %+v", rerr)
	}
}

func TestProjectAdd_MalformedParams_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewProjectAdd(&pendingStoreCapture{}, true)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{not json`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectAdd_EmptyID_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewProjectAdd(&pendingStoreCapture{}, true)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":""}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectAdd_StoreError_ReturnsInternal(t *testing.T) {
	store := &pendingStoreCapture{err: errors.New("io fail")}
	m := methods.NewProjectAdd(store, true)
	_, rerr := m.Handle(opCtx(t, "U_ALICE"), json.RawMessage(`{"id":"alpha"}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError, got %+v", rerr)
	}
}

// compile-time assertion
var _ usecaserpc.Method = (*methods.ProjectAdd)(nil)
