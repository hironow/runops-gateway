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

// fakeProjectRegistry is a minimal port.ProjectRegistry for method tests.
type fakeProjectRegistry struct {
	getResult domain.Project
	getErr    error
	getCalls  int
	gotID     string
}

func (f *fakeProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	return errors.New("not used in test")
}
func (f *fakeProjectRegistry) List(_ context.Context, _ port.ProjectListFilter) ([]domain.Project, error) {
	return nil, errors.New("not used in test")
}
func (f *fakeProjectRegistry) Get(_ context.Context, id string) (domain.Project, error) {
	f.getCalls++
	f.gotID = id
	return f.getResult, f.getErr
}
func (f *fakeProjectRegistry) Archive(_ context.Context, _ string) error {
	return errors.New("not used in test")
}

func TestProjectGet_Name(t *testing.T) {
	// given
	m := methods.NewProjectGet(&fakeProjectRegistry{})

	// then
	if got := m.Name(); got != "runops.admin.project.get" {
		t.Errorf("Name: got %q, want %q", got, "runops.admin.project.get")
	}
}

func TestProjectGet_HappyPath_ReturnsProject(t *testing.T) {
	// given
	want := domain.Project{
		ID:                  "alpha",
		GitHubOrg:           "acme",
		GitHubRepo:          "alpha-repo",
		WorkspacePath:       "/srv/projects/alpha",
		SlackDefaultChannel: "C12345",
		Status:              domain.ProjectStatusActive,
		CreatedAt:           time.Now().UTC().Truncate(time.Second),
	}
	reg := &fakeProjectRegistry{getResult: want}
	m := methods.NewProjectGet(reg)

	// when
	result, rerr := m.Handle(context.Background(), json.RawMessage(`{"id":"alpha"}`))

	// then
	if rerr != nil {
		t.Fatalf("Handle returned error: %+v", rerr)
	}
	if reg.gotID != "alpha" {
		t.Errorf("registry.Get id: got %q, want %q", reg.gotID, "alpha")
	}
	out, _ := json.Marshal(result)
	var got map[string]domain.Project
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-decode result failed: %v", err)
	}
	if got["project"].ID != want.ID {
		t.Errorf("project.id: got %q, want %q", got["project"].ID, want.ID)
	}
}

func TestProjectGet_EmptyID_ReturnsInvalidParams(t *testing.T) {
	// given
	m := methods.NewProjectGet(&fakeProjectRegistry{})

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"id":""}`))

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectGet_MalformedParams_ReturnsInvalidParams(t *testing.T) {
	// given
	m := methods.NewProjectGet(&fakeProjectRegistry{})

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{not json`))

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectGet_NotFound_ReturnsApplicationError(t *testing.T) {
	// given - registry returns ErrProjectNotFound
	reg := &fakeProjectRegistry{getErr: domain.ErrProjectNotFound}
	m := methods.NewProjectGet(reg)

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"id":"nope"}`))

	// then
	if rerr == nil {
		t.Fatalf("expected error for not-found, got nil")
	}
	// Application errors are in the reserved range -32000 〜 -32099.
	if rerr.Code > domainrpc.CodeApplicationErrorBase || rerr.Code < -32099 {
		t.Errorf("expected application error code, got %d", rerr.Code)
	}
}

func TestProjectGet_OperatorContextAvailable(t *testing.T) {
	// given - operator carried in context (= §B-3 contract)
	want := domain.Project{ID: "alpha", Status: domain.ProjectStatusActive}
	reg := &fakeProjectRegistry{getResult: want}
	m := methods.NewProjectGet(reg)
	op, _ := domainrpc.NewOperator("U_ALICE", "alice@example.com")
	ctx := usecaserpc.WithOperator(context.Background(), op)

	// when
	_, rerr := m.Handle(ctx, json.RawMessage(`{"id":"alpha"}`))

	// then - no error; method doesn't strictly require operator but must not panic
	if rerr != nil {
		t.Errorf("Handle with operator failed: %+v", rerr)
	}
}

// compile-time assertion: *ProjectGet satisfies usecaserpc.Method
var _ usecaserpc.Method = (*methods.ProjectGet)(nil)
