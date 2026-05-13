package methods

import (
	"context"
	"encoding/json"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
)

const methodNameProjectList = "runops.admin.project.list"

// ProjectList handles `runops.admin.project.list`. Optional status filter:
// "active" (default) | "archived" | "all". Read-only; no approval gate.
type ProjectList struct {
	registry port.ProjectRegistry
}

// NewProjectList wires a ProjectList method.
func NewProjectList(registry port.ProjectRegistry) *ProjectList {
	if registry == nil {
		panic("methods.NewProjectList: registry must not be nil")
	}
	return &ProjectList{registry: registry}
}

// Name returns the JSON-RPC method name.
func (m *ProjectList) Name() string { return methodNameProjectList }

// projectListParams parses optional status filter.
type projectListParams struct {
	Status string `json:"status"`
}

// Handle decodes params, resolves the status filter, and returns
// {projects: [Project]}.
func (m *ProjectList) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	var p projectListParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &domainrpc.Error{
				Code:    domainrpc.CodeInvalidParams,
				Message: "invalid params: " + err.Error(),
			}
		}
	}

	filter, perr := resolveListFilter(p.Status)
	if perr != nil {
		return nil, perr
	}

	logOperator(ctx, methodNameProjectList, "status", p.Status)

	projects, err := m.registry.List(ctx, filter)
	if err != nil {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "registry.List failed",
		}
	}
	// Always return a non-nil slice so the wire format is `[]`, not `null`.
	if projects == nil {
		projects = []domain.Project{}
	}
	return map[string]any{"projects": projects}, nil
}

// resolveListFilter maps the user-supplied status string to a ProjectListFilter.
// Empty input defaults to "active" (= the most common admin read path).
// "all" maps to an empty status which the registry treats as "any".
func resolveListFilter(status string) (port.ProjectListFilter, *domainrpc.Error) {
	switch status {
	case "", "active":
		return port.ProjectListFilter{Status: domain.ProjectStatusActive}, nil
	case "archived":
		return port.ProjectListFilter{Status: domain.ProjectStatusArchived}, nil
	case "all":
		return port.ProjectListFilter{}, nil
	default:
		return port.ProjectListFilter{}, &domainrpc.Error{
			Code:    domainrpc.CodeInvalidParams,
			Message: "status must be 'active', 'archived', or 'all'",
		}
	}
}
