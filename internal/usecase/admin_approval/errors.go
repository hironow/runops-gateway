// Package admin_approval implements the orchestrator that picks up
// admin-mutation approval-ack signals (= ADR 0040 §approval gate
// integration steps 6-8), validates the ADR 0035 4-eyes invariant, and
// applies the corresponding mutation to the project registry.
//
// The orchestrator is the consumer side of the admin-mutation flow:
//
//  1. mutation method (= §B-5.2 ProjectAdd / ProjectArchive) creates a
//     PendingApproval keyed by IdempotencyKey
//  2. (§B-5.4-ish) producer side publishes a convergence D-Mail so a
//     Slack channel surfaces the approve / deny buttons
//  3. operator clicks approve → Slack handler routes here via
//     OnApprovalAck(ctx, key, approver, approverType)
//  4. orchestrator validates 4-eyes (= effective_requester_id != approver_id
//     AND approverType == human-operator)
//  5. orchestrator applies the mutation to ProjectRegistry
//  6. orchestrator transitions PendingStore to approved_applied
//
// Deny / timeout path uses OnApprovalDeny / OnApprovalTimeout (= same
// transition logic, no apply step).
//
// The package depends ONLY on core/domain and core/port; no adapter
// imports are allowed (= layer architecture, semgrep-enforced).
package admin_approval

import "errors"

// Sentinel errors returned by orchestrator operations. Callers SHOULD
// use errors.Is for branching; the orchestrator never panics on a
// missing or terminal pending — that surface is reserved for
// programmer-only bugs (= nil registry / nil store at construction).
var (
	// ErrPendingMissing is returned when the IdempotencyKey is not
	// known to the store. Distinct from a generic store error so the
	// caller can log "stale Slack click" without alerting on real
	// infrastructure failures.
	ErrPendingMissing = errors.New("admin_approval: pending approval not found")

	// ErrSelfApproval signals an ADR 0035 4-eyes invariant violation:
	// the approver clicked their own pending request. Logged at WARN
	// and rejected; the pending stays in pending_approval status so a
	// second operator can still approve.
	ErrSelfApproval = errors.New("admin_approval: approver matches requester (4-eyes invariant)")

	// ErrApproverActorNotHuman signals that the approver was not
	// classified as human-operator. Admin mutations require a
	// human-on-human-approver gate; AI agent approvers are forbidden.
	ErrApproverActorNotHuman = errors.New("admin_approval: approver actor type must be human-operator")

	// ErrUnresolvedRequester signals an absent EffectiveRequesterID
	// (= legacy ADR 0039 record migrated with empty value). The
	// orchestrator fails closed: it cannot guarantee 4-eyes without
	// a known requester identity, so it refuses to apply.
	ErrUnresolvedRequester = errors.New("admin_approval: pending record has no effective_requester_id")

	// ErrAlreadyTerminal signals that the pending has already been
	// resolved (= approved_applied / denied / timeout). Treated as
	// idempotent success by some callers (= duplicate Slack clicks)
	// and as an error by others (= testing harness). Callers decide.
	ErrAlreadyTerminal = errors.New("admin_approval: pending is already in a terminal state")
)
