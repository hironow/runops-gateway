package methods

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
	usecaserpc "github.com/hironow/runops-gateway/internal/usecase/rpc"
)

// ProjectAdd handles `runops.admin.project.add` (= HIGH severity, ADR 0040
// §method 命名規約). The method does NOT mutate ProjectRegistry directly.
// It records a PendingApproval keyed by a deterministic IdempotencyKey
// and returns `{idempotency_key, status: "pending_approval"}`. The admin
// approval orchestrator (§B-5.3) picks up the approval-ack, validates
// the 4-eyes invariant (ADR 0035 carry), and only then writes the
// project to the registry.
//
// flagEnabled gates the entire flow (= RUNOPS_RPC_HIGH_MUTATION_ENABLED
// in the wiring layer). With flag off the method returns an application
// error (-32000) so callers see a clear "feature gated" signal instead
// of -32601 "method not found".
//
// approval optionally publishes a convergence D-Mail to the configured
// Slack channel so an operator sees the approve / deny buttons (= §B-5.4b
// wire-up). When unset, mutation methods skip publishing but still
// record the pending; operators can trigger a manual repost later.
type ProjectAdd struct {
	store       port.PendingStore
	flagEnabled bool
	approval    *approvalPublisher
}

// NewProjectAdd wires a ProjectAdd method.
func NewProjectAdd(store port.PendingStore, flagEnabled bool) *ProjectAdd {
	if store == nil {
		panic("methods.NewProjectAdd: store must not be nil")
	}
	return &ProjectAdd{store: store, flagEnabled: flagEnabled}
}

// WithApprovalPublisher attaches the convergence-D-Mail publisher used
// to surface approve / deny buttons in Slack. Returns the receiver so
// the wiring layer can chain construction.
func (m *ProjectAdd) WithApprovalPublisher(req port.ApprovalRequester, target port.NotifyTarget) *ProjectAdd {
	m.approval = &approvalPublisher{requester: req, target: target}
	return m
}

// Name returns the JSON-RPC method name.
func (m *ProjectAdd) Name() string { return MethodNameProjectAdd }

// projectAddParams mirrors the existing REST admin endpoint body so
// operators can migrate cleanly from /admin/projects POST to /rpc.
type projectAddParams struct {
	ID                      string `json:"id"`
	GitHubOrg               string `json:"github_org"`
	GitHubRepo              string `json:"github_repo"`
	WorkspacePath           string `json:"workspace_path"`
	SlackDefaultChannel     string `json:"slack_default_channel,omitempty"`
	GitHubAppInstallationID int64  `json:"github_app_installation_id,omitempty"`
}

// Handle validates params, computes the IdempotencyKey, and records the
// PendingApproval. Idempotent: a duplicate (= same operator + same
// canonical params) returns the existing pending key with the same
// envelope shape.
func (m *ProjectAdd) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	if !m.flagEnabled {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeApplicationErrorBase,
			Message: "HIGH mutation disabled (RUNOPS_RPC_HIGH_MUTATION_ENABLED unset)",
		}
	}

	op, ok := usecaserpc.OperatorFromContext(ctx)
	if !ok {
		// Should not happen behind the §B-3 auth middleware; defensive.
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "operator identity missing from context",
		}
	}

	var p projectAddParams
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

	logOperator(ctx, MethodNameProjectAdd, "id", p.ID)
	return createPending(ctx, m.store, op, MethodNameProjectAdd, domain.PendingOpAdd, params, m.approval)
}

// createPending is shared by ProjectAdd / ProjectArchive (= identical
// flow: derive IdempotencyKey + CreateIfNotExists + publish approval
// D-Mail + emit `{idempotency_key, status}`). Pulled out so the two
// methods stay thin and the 4-eyes contract has a single audit surface.
//
// approval is optional. When non-nil it publishes a convergence D-Mail
// to the configured Slack channel so an operator sees the approve / deny
// buttons. A publish failure does NOT fail the method — the pending
// record is the authoritative source of truth, so the caller still
// receives `{idempotency_key, status: "pending_approval"}` and operators
// can re-trigger the Slack post manually.
func createPending(
	ctx context.Context,
	store port.PendingStore,
	op domainrpc.Operator,
	method string,
	pendingOp domain.PendingOp,
	rawParams json.RawMessage,
	approval *approvalPublisher,
) (any, *domainrpc.Error) {
	key, err := ComputeIdempotencyKey(op.OperatorID, method, rawParams)
	if err != nil {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "compute idempotency key: " + err.Error(),
		}
	}
	pending := domain.PendingApproval{
		IdempotencyKey:       key,
		Op:                   pendingOp,
		BodyJSON:             []byte(rawParams),
		EffectiveRequesterID: op.OperatorID,
		RequesterActorType:   string(op.ActorType),
		CreatedAt:            time.Now().UTC(),
		Status:               domain.PendingStatusPendingApproval,
	}
	saved, err := store.CreateIfNotExists(ctx, pending)
	newlyCreated := false
	switch {
	case err == nil:
		newlyCreated = true
	case errors.Is(err, port.ErrPendingAlreadyExists):
		// duplicate — return the existing record's identity so the
		// caller observes the same envelope shape (= idempotent retry).
		pending = saved
	default:
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "create pending approval: " + err.Error(),
		}
	}

	// Publish the convergence D-Mail only for freshly created pendings.
	// Idempotent retries skip the publish to avoid double-posting to
	// Slack; operators can manually repost from the original Slack
	// message if needed.
	if newlyCreated {
		approval.publish(ctx, method, pending)
	}

	return map[string]any{
		"idempotency_key": pending.IdempotencyKey,
		"status":          string(domain.PendingStatusPendingApproval),
	}, nil
}
