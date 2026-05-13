package methods

import (
	"context"
	"encoding/json"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// ProjectArchive handles `runops.admin.project.archive` (= HIGH severity).
// Records a PendingApproval for archive and defers the actual mutation
// to the §B-5.3 admin approval orchestrator, mirroring ProjectAdd.
type ProjectArchive struct {
	store       port.PendingStore
	flagEnabled bool
	approval    *approvalPublisher
}

// NewProjectArchive wires a ProjectArchive method.
func NewProjectArchive(store port.PendingStore, flagEnabled bool) *ProjectArchive {
	if store == nil {
		panic("methods.NewProjectArchive: store must not be nil")
	}
	return &ProjectArchive{store: store, flagEnabled: flagEnabled}
}

// WithApprovalPublisher attaches the convergence-D-Mail publisher,
// mirroring ProjectAdd.
func (m *ProjectArchive) WithApprovalPublisher(req port.ApprovalRequester, target port.NotifyTarget) *ProjectArchive {
	m.approval = &approvalPublisher{requester: req, target: target}
	return m
}

// Name returns the JSON-RPC method name.
func (m *ProjectArchive) Name() string { return MethodNameProjectArchive }

// projectArchiveParams mirrors the REST `/admin/projects/{id}/archive` URL
// parameter as a JSON body field for transport homogeneity.
type projectArchiveParams struct {
	ID string `json:"id"`
}

// Handle decodes the id, computes the IdempotencyKey, and records a
// PendingApproval (op=archive).
func (m *ProjectArchive) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	if !m.flagEnabled {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeApplicationErrorBase,
			Message: "HIGH mutation disabled (RUNOPS_RPC_HIGH_MUTATION_ENABLED unset)",
		}
	}

	op, ok := usecaserpc.OperatorFromContext(ctx)
	if !ok {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "operator identity missing from context",
		}
	}

	var p projectArchiveParams
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

	logOperator(ctx, MethodNameProjectArchive, "id", p.ID)
	return createPending(ctx, m.store, op, MethodNameProjectArchive, domain.PendingOpArchive, params, m.approval)
}
