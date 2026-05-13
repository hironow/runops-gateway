package methods_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

// fakePendingStore satisfies port.PendingStore for tests.
type fakePendingStore struct {
	getResult domain.PendingApproval
	getErr    error
	gotKey    string
}

func (f *fakePendingStore) CreateIfNotExists(_ context.Context, _ domain.PendingApproval) (domain.PendingApproval, error) {
	return domain.PendingApproval{}, errors.New("not used in test")
}
func (f *fakePendingStore) Get(_ context.Context, key string) (domain.PendingApproval, error) {
	f.gotKey = key
	return f.getResult, f.getErr
}
func (f *fakePendingStore) Transition(_ context.Context, _ string, _ domain.PendingStatus, _ *time.Time) error {
	return errors.New("not used in test")
}

func TestPendingGet_Name(t *testing.T) {
	m := methods.NewPendingGet(&fakePendingStore{})
	if got := m.Name(); got != "runops.admin.project.pending.get" {
		t.Errorf("Name: got %q", got)
	}
}

func TestPendingGet_HappyPath_OmitsBodyJSON(t *testing.T) {
	// given - the stored PendingApproval has body_json that MUST NOT leak.
	appliedAt := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	stored := domain.PendingApproval{
		IdempotencyKey: "abc123",
		Op:             domain.PendingOpAdd,
		BodyJSON:       []byte(`{"secret":"do-not-leak"}`),
		CreatedAt:      time.Date(2026, 5, 13, 11, 0, 0, 0, time.UTC),
		Status:         domain.PendingStatusApprovedApplied,
		AppliedAt:      &appliedAt,
	}
	store := &fakePendingStore{getResult: stored}
	m := methods.NewPendingGet(store)

	// when
	result, rerr := m.Handle(context.Background(), json.RawMessage(`{"idempotency_key":"abc123"}`))

	// then
	if rerr != nil {
		t.Fatalf("Handle returned error: %+v", rerr)
	}
	out, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	wire := string(out)
	if strings.Contains(wire, "body_json") || strings.Contains(wire, "do-not-leak") {
		t.Errorf("body_json must be excluded from response; got: %s", wire)
	}
	if !strings.Contains(wire, `"idempotency_key":"abc123"`) {
		t.Errorf("idempotency_key must be present; got: %s", wire)
	}
	if !strings.Contains(wire, `"status":"approved_applied"`) {
		t.Errorf("status must be present; got: %s", wire)
	}
	if !strings.Contains(wire, `"op":"add"`) {
		t.Errorf("op must be present; got: %s", wire)
	}
	if !strings.Contains(wire, "applied_at") {
		t.Errorf("applied_at must be present when set; got: %s", wire)
	}
}

func TestPendingGet_AppliedAtOmittedWhenNil(t *testing.T) {
	// given - status pending_approval、 applied_at not yet set
	stored := domain.PendingApproval{
		IdempotencyKey: "abc123",
		Op:             domain.PendingOpAdd,
		Status:         domain.PendingStatusPendingApproval,
	}
	m := methods.NewPendingGet(&fakePendingStore{getResult: stored})

	// when
	result, rerr := m.Handle(context.Background(), json.RawMessage(`{"idempotency_key":"abc123"}`))
	if rerr != nil {
		t.Fatalf("Handle: %+v", rerr)
	}
	out, _ := json.Marshal(result)

	// then
	if strings.Contains(string(out), "applied_at") {
		t.Errorf("applied_at must be omitted when nil; got: %s", out)
	}
}

func TestPendingGet_EmptyKey_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewPendingGet(&fakePendingStore{})
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"idempotency_key":""}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestPendingGet_MalformedParams_ReturnsInvalidParams(t *testing.T) {
	m := methods.NewPendingGet(&fakePendingStore{})
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{not json`))
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestPendingGet_NotFound_ReturnsApplicationError(t *testing.T) {
	store := &fakePendingStore{getErr: port.ErrPendingNotFound}
	m := methods.NewPendingGet(store)
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"idempotency_key":"missing"}`))
	if rerr == nil {
		t.Fatalf("expected error for not-found, got nil")
	}
	if rerr.Code > domainrpc.CodeApplicationErrorBase || rerr.Code < -32099 {
		t.Errorf("expected application error code, got %d", rerr.Code)
	}
}

func TestPendingGet_StoreError_ReturnsInternal(t *testing.T) {
	store := &fakePendingStore{getErr: errors.New("io fail")}
	m := methods.NewPendingGet(store)
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"idempotency_key":"abc"}`))
	if rerr == nil || rerr.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError, got %+v", rerr)
	}
}

func TestPendingGet_OperatorContextAvailable(t *testing.T) {
	stored := domain.PendingApproval{IdempotencyKey: "k", Op: domain.PendingOpAdd, Status: domain.PendingStatusPendingApproval}
	m := methods.NewPendingGet(&fakePendingStore{getResult: stored})
	op, _ := domainrpc.NewOperator("U_ALICE", "alice@example.com")
	ctx := usecaserpc.WithOperator(context.Background(), op)
	_, rerr := m.Handle(ctx, json.RawMessage(`{"idempotency_key":"k"}`))
	if rerr != nil {
		t.Errorf("Handle with operator failed: %+v", rerr)
	}
}

// compile-time assertion
var _ usecaserpc.Method = (*methods.PendingGet)(nil)
