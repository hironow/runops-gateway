package methods

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
)

const methodNamePendingGet = "runops.admin.project.pending.get"

const codePendingNotFound = -32002

// PendingGet handles `runops.admin.project.pending.get` — returns the
// approval-flow state for a given idempotency_key. Read-only.
//
// The response intentionally OMITS `body_json` (= the snapshot of the
// original mutation request body). Exposing it via a read-only endpoint
// would leak content that may include operator-supplied secrets before
// the 4-eyes approval step completes.
type PendingGet struct {
	store port.PendingStore
}

// NewPendingGet wires a PendingGet method to the given store.
func NewPendingGet(store port.PendingStore) *PendingGet {
	if store == nil {
		panic("methods.NewPendingGet: store must not be nil")
	}
	return &PendingGet{store: store}
}

// Name returns the JSON-RPC method name.
func (m *PendingGet) Name() string { return methodNamePendingGet }

// pendingGetParams parses the idempotency_key from the params object.
type pendingGetParams struct {
	IdempotencyKey string `json:"idempotency_key"`
}

// pendingGetResult is the wire-format response.
//
// Distinct from domain.PendingApproval to (a) exclude BodyJSON and
// RequesterActorType from the read-only view, and (b) keep the wire
// shape stable even if the domain type grows internal fields later.
type pendingGetResult struct {
	IdempotencyKey string     `json:"idempotency_key"`
	Op             string     `json:"op"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	AppliedAt      *time.Time `json:"applied_at,omitempty"`
}

// Handle decodes params, fetches the PendingApproval, and returns the
// redacted view.
func (m *PendingGet) Handle(ctx context.Context, params json.RawMessage) (any, *domainrpc.Error) {
	var p pendingGetParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &domainrpc.Error{
				Code:    domainrpc.CodeInvalidParams,
				Message: "invalid params: " + err.Error(),
			}
		}
	}
	if p.IdempotencyKey == "" {
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInvalidParams,
			Message: "idempotency_key is required",
		}
	}

	logOperator(ctx, methodNamePendingGet, "idempotency_key", p.IdempotencyKey)

	pa, err := m.store.Get(ctx, p.IdempotencyKey)
	if err != nil {
		if errors.Is(err, port.ErrPendingNotFound) {
			return nil, &domainrpc.Error{
				Code:    codePendingNotFound,
				Message: "pending approval not found",
			}
		}
		return nil, &domainrpc.Error{
			Code:    domainrpc.CodeInternalError,
			Message: "store.Get failed",
		}
	}

	return redactPendingApproval(pa), nil
}

// redactPendingApproval builds the wire response, stripping BodyJSON and
// RequesterActorType so neither is exposed via the read-only endpoint.
func redactPendingApproval(pa domain.PendingApproval) pendingGetResult {
	return pendingGetResult{
		IdempotencyKey: pa.IdempotencyKey,
		Op:             string(pa.Op),
		Status:         string(pa.Status),
		CreatedAt:      pa.CreatedAt,
		AppliedAt:      pa.AppliedAt,
	}
}
