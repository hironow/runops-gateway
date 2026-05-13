package methods_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

// listProjectRegistry is a port.ProjectRegistry that records List calls.
type listProjectRegistry struct {
	listResult []domain.Project
	listErr    error
	gotFilter  port.ProjectListFilter
	listCalls  int
}

func (f *listProjectRegistry) Add(_ context.Context, _ domain.Project) error {
	return errors.New("not used in test")
}
func (f *listProjectRegistry) List(_ context.Context, filter port.ProjectListFilter) ([]domain.Project, error) {
	f.listCalls++
	f.gotFilter = filter
	return f.listResult, f.listErr
}
func (f *listProjectRegistry) Get(_ context.Context, _ string) (domain.Project, error) {
	return domain.Project{}, errors.New("not used in test")
}
func (f *listProjectRegistry) Archive(_ context.Context, _ string) error {
	return errors.New("not used in test")
}

func TestProjectList_Name(t *testing.T) {
	// given
	m := methods.NewProjectList(&listProjectRegistry{})

	// then
	if got := m.Name(); got != "runops.admin.project.list" {
		t.Errorf("Name: got %q", got)
	}
}

func TestProjectList_DefaultStatusActive(t *testing.T) {
	// given - no params / null params → filter status = "active"
	reg := &listProjectRegistry{listResult: []domain.Project{{ID: "a"}, {ID: "b"}}}
	m := methods.NewProjectList(reg)

	// when
	result, rerr := m.Handle(context.Background(), nil)

	// then
	if rerr != nil {
		t.Fatalf("Handle returned error: %+v", rerr)
	}
	if reg.gotFilter.Status != domain.ProjectStatusActive {
		t.Errorf("filter.Status default: got %q, want %q", reg.gotFilter.Status, domain.ProjectStatusActive)
	}
	out, _ := json.Marshal(result)
	var got map[string][]domain.Project
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-decode failed: %v", err)
	}
	if len(got["projects"]) != 2 {
		t.Errorf("projects count: got %d, want 2", len(got["projects"]))
	}
}

func TestProjectList_StatusFilterValues(t *testing.T) {
	cases := map[string]domain.ProjectStatus{
		"active":   domain.ProjectStatusActive,
		"archived": domain.ProjectStatusArchived,
		"all":      "", // "all" → empty status (= ProjectListFilter.Status == "" means any)
	}
	for input, want := range cases {
		t.Run("status="+input, func(t *testing.T) {
			// given
			reg := &listProjectRegistry{listResult: []domain.Project{}}
			m := methods.NewProjectList(reg)
			params := json.RawMessage(`{"status":"` + input + `"}`)

			// when
			_, rerr := m.Handle(context.Background(), params)

			// then
			if rerr != nil {
				t.Fatalf("Handle returned error: %+v", rerr)
			}
			if reg.gotFilter.Status != want {
				t.Errorf("filter.Status: got %q, want %q", reg.gotFilter.Status, want)
			}
		})
	}
}

func TestProjectList_InvalidStatus_ReturnsInvalidParams(t *testing.T) {
	// given - status not in {active, archived, all, ""}
	reg := &listProjectRegistry{}
	m := methods.NewProjectList(reg)

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{"status":"nope"}`))

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
	if reg.listCalls != 0 {
		t.Errorf("registry.List should not be invoked on invalid params; got %d calls", reg.listCalls)
	}
}

func TestProjectList_MalformedParams_ReturnsInvalidParams(t *testing.T) {
	// given
	m := methods.NewProjectList(&listProjectRegistry{})

	// when
	_, rerr := m.Handle(context.Background(), json.RawMessage(`{not json`))

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", rerr)
	}
}

func TestProjectList_RegistryError_ReturnsInternal(t *testing.T) {
	// given
	reg := &listProjectRegistry{listErr: errors.New("io fail")}
	m := methods.NewProjectList(reg)

	// when
	_, rerr := m.Handle(context.Background(), nil)

	// then
	if rerr == nil || rerr.Code != domainrpc.CodeInternalError {
		t.Errorf("expected CodeInternalError, got %+v", rerr)
	}
}

func TestProjectList_OperatorContextAvailable(t *testing.T) {
	// given
	reg := &listProjectRegistry{listResult: []domain.Project{}}
	m := methods.NewProjectList(reg)
	op, _ := domainrpc.NewOperator("U_ALICE", "alice@example.com")
	ctx := usecaserpc.WithOperator(context.Background(), op)

	// when
	_, rerr := m.Handle(ctx, nil)

	// then
	if rerr != nil {
		t.Errorf("Handle with operator failed: %+v", rerr)
	}
}

// compile-time assertion
var _ usecaserpc.Method = (*methods.ProjectList)(nil)
