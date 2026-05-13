package methods

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// approvalPublisher wraps a port.ApprovalRequester with the fixed
// NotifyTarget that admin mutations should post to. mutation methods
// optionally hold one; nil means "publishing disabled" (= still records
// pending, but operators must repost the Slack buttons manually).
//
// Producer side of the ADR 0040 §B-5 admin approval flow. Mirrors the
// Phase 4a HIGH-severity convergence shape so handleApprovalAction can
// reuse the same parser; the Kind=admin_mutation metadata routes the
// click to the admin orchestrator instead of the convergence applicator.
type approvalPublisher struct {
	requester port.ApprovalRequester
	target    port.NotifyTarget
}

// publish posts a convergence D-Mail with the admin-mutation metadata
// payload. Best-effort: a publish failure is logged and swallowed so
// the pending record remains the source of truth (= operator can repost
// manually). The dispatcher result envelope therefore reflects "pending
// created" even if Slack rendering momentarily fails.
func (p *approvalPublisher) publish(
	ctx context.Context,
	method string,
	pa domain.PendingApproval,
) {
	if p == nil || p.requester == nil {
		return
	}
	mail := buildAdminApprovalDMail(method, pa)
	if err := p.requester.PostApprovalRequest(ctx, p.target, mail); err != nil {
		slog.WarnContext(ctx,
			"admin mutation: approval D-Mail publish failed (pending stays in pending_approval)",
			"method", method,
			"idempotency_key", pa.IdempotencyKey,
			"error", err.Error())
	}
}

// buildAdminApprovalDMail builds the convergence D-Mail payload for an
// admin approval request. The Body is short and operator-facing; the
// Metadata is what handleApprovalAction + ApprovalRequester consume to
// build the buttons.
func buildAdminApprovalDMail(method string, pa domain.PendingApproval) domain.DMail {
	body := fmt.Sprintf(
		"Admin approval requested\n\n- method: `%s`\n- op: `%s`\n- requester: `%s`\n- idempotency_key: `%s`",
		method, pa.Op, pa.EffectiveRequesterID, pa.IdempotencyKey,
	)
	return domain.DMail{
		Kind:   domain.DMailKindConvergence,
		Source: "runops-gateway",
		Target: "admin-approver",
		Body:   body,
		Metadata: map[string]string{
			"kind":                                 "admin_mutation",
			"parent_idempotency_key":               pa.IdempotencyKey,
			"requester_id":                         pa.EffectiveRequesterID,
			domain.MetadataKeyRequesterActorType:   pa.RequesterActorType,
			domain.MetadataKeyRequesterActorSource: "env_attested",
		},
	}
}
