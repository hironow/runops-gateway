// Package methods implements concrete JSON-RPC Method handlers for the
// admin endpoint (= ADR 0040 §method 命名規約 carry).
//
// Each method satisfies internal/usecase/rpc.Method:
//   - Name() string returns the JSON-RPC method name
//   - Handle(ctx, params) (any, *Error) executes the call
//
// Methods depend only on port interfaces + domain types, NOT on adapter
// concrete types (= layer architecture: usecase → port + domain only).
// Operator identity is carried via ctx (= §B-3 usecaserpc.WithOperator).
package methods

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// Application error codes — ADR 0040 §JSON-RPC 2.0 spec reserves
// [-32099, -32000] for application-defined errors.
const (
	codeProjectNotFound = -32001
)

// ProjectGet handles `runops.admin.project.get` — returns a single Project
// by id. Read-only; does not require approval gate.
type ProjectGet struct {
	registry port.ProjectRegistry
}

// NewProjectGet wires a ProjectGet method to the given registry.
func NewProjectGet(registry port.ProjectRegistry) *ProjectGet {
	if registry == nil {
		panic("methods.NewProjectGet: registry must not be nil")
	}
	return &ProjectGet{registry: registry}
}

// Name returns the JSON-RPC method name.
func (m *ProjectGet) Name() string { return MethodNameProjectGet }

// projectGetParams is the parsed shape of the params object.
type projectGetParams struct {
	ID string `json:"id"`
}

// Handle decodes params and resolves the project by id.
func (m *ProjectGet) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	var p projectGetParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &domainrpc.Error{
				Code:    domainrpc.CodeInvalidParams,
				Message: "invalid params: " + err.Error(),
			}
		}
	}
	if p.ID == "" {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInvalidParams,
			Message: "id is required",
		}
	}

	logOperator(ctx, MethodNameProjectGet, "id", p.ID)

	proj, err := m.registry.Get(ctx, p.ID)
	if err != nil {
		if errors.Is(err, domain.ErrProjectNotFound) {
			return nil, &domainrpc.Error{
				Code:    codeProjectNotFound,
				Message: "project not found",
			}
		}
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "registry.Get failed",
		}
	}
	return map[string]any{"project": proj}, nil
}

// logOperator emits a constant-key audit log for read-only access.
// Operator identity is best-effort: missing operator does not block the call,
// but the log captures whichever identity was injected by §B-3 transport.
func logOperator(ctx context.Context, method string, kv ...any) {
	op, ok := usecaserpc.OperatorFromContext(ctx)
	args := []any{"method", method}
	if ok {
		args = append(args, "operator_id", op.OperatorID, "actor_type", string(op.ActorType))
	} else {
		args = append(args, "operator_id", "<unknown>")
	}
	args = append(args, kv...)
	slog.InfoContext(ctx, "rpc method invoked", args...)
}
