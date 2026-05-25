# 0036. Phase 4a approval path actor-type validation (extends ADR 0035, refs#0011)

**Date:** 2026-05-08
**Status:** Accepted

## Context

ADR 0035 (AI agent cannot approve AI agent, 2026-05-08) §Layer 2 specifies
the use-case orchestration layer for approval validation:

> `usecase.RunOpsService.ApproveAction` calls `ValidateApproverPermitted`
> BEFORE `auth.IsAuthorized` so the AI-vs-AI rejection produces a precise
> audit signal.

Phase A-1 (PR #91) and Phase A-2 (PR #92) implemented this exact chain:
domain validator + sentinel + use-case integration. They cover the
**dispatch / canary deploy** approval path that flows through
`ApproveAction`.

There is a second approval path that ADR 0035 §Layer 2 did not address
explicitly: the **Phase 4a 4-eyes approval flow** (ADR 0019). When a
HIGH-severity convergence DMail arrives, the gateway posts approve / deny
buttons via `ApprovalRequester`; on click, `handleApprovalAction`
publishes an approval-ack DMail directly through `approvalPublisher`,
**bypassing `ApproveAction` entirely**. The `approvalActionValue`
button payload that survives the click currently does not carry the
original requester's actor type, so the Phase 4a path cannot apply
ADR 0035's invariant — even with Phase A-1/A-2/A-3 landed.

Concretely (per `internal/adapter/input/slack/handler.go::handleApprovalAction`):

```text
button click → parseApprovalActionValue → 4-eyes guard (clicker != requester)
            → consumedTokens replay guard
            → approvalPublisher.PublishDMail(ack)
```

There is no actor-type validation step. An AI agent enrolled via
`SLACK_AI_AGENT_BOT_USER_IDS` (Phase A-3) clicking Approve on a
HIGH-severity convergence DMail emitted by another AI agent would
currently land the ack unchecked.

## Decision

Extend ADR 0035 §Layer 2 to the Phase 4a path with **four carry points**,
mirroring the carry-point discipline of ADR 0027 (project_id metadata
through Slack → Pub/Sub):

### Carry point 1 — DMail.Metadata key

The DMail Metadata map gains a canonical key
`requester_actor_type` whose value is one of the four
`domain.CallerType` enum strings (`human-operator`, `ai-agent`,
`gateway-service`, `workspace-daemon`). Empty key, empty value, or
absence ⇒ treated as `human-operator` during the migration window
(see Migration below).

### Carry point 2 — `approvalActionValue` field

`internal/adapter/input/slack/approval_action.go::approvalActionValue`
gains a `RequesterActorType string` field with json tag
`requester_actor_type`. `buildButtonValues`
(`internal/adapter/output/slack/approval_requester.go`) reads
`mail.Metadata["requester_actor_type"]` and writes it into the
button payload.

### Carry point 3 — `handleApprovalAction` validator

After the existing 4-eyes guard but before the consumed-token check,
`handleApprovalAction` resolves the clicker's actor type via the
existing `ClassifyApproverActorType(clickerUserID, h.aiAgentBotUserIDs)`
(the same classifier introduced in Phase A-3) and calls
`domain.ValidateApproverPermitted` with a synthetic `ApprovalRequest`
constructed from `av.RequesterActorType`. On
`ErrAIAgentCannotApproveAIAgent` the handler:

- writes a `slog.Warn("ai_approves_ai_attempt", ...)` audit log
  (mirroring Phase A-2 §Layer 2 audit shape), and
- sends an ephemeral notice to the clicker, and
- returns without publishing the ack DMail.

Unknown (non-empty, non-canonical) `RequesterActorType` values are
rejected outright with a similar audit log
(`approval_actor_type_invalid`) — fail-closed for any value the
gateway cannot interpret.

### Carry point 4 — Approval ack metadata

The approval ack DMail published by `handleApprovalAction` SHOULD also
include `requester_actor_type` in its Metadata so the original producer
can correlate the approval back to the actor classification it set.
This is non-blocking for ADR 0036 acceptance but explicitly enumerated
so future implementers do not silently drop the key on the ack edge.

## Migration

Producers (workspace tools: phonewave, sightjack, paintress, amadeus)
do not yet emit the `requester_actor_type` metadata key. ADR 0036 lives
in the gateway and only governs gateway-side handling. The migration
window is:

- **2026-05-08 → 2026-05-31** (24 days): empty / absent
  `RequesterActorType` is treated as `human-operator`. Producers SHOULD
  start emitting the key during this window.
- **From 2026-06-01**: the gateway begins logging
  `slog.Warn("approval_actor_type_missing", ...)` for empty values
  while still falling back to `human-operator` (no behavioural change yet).
- **A future ADR (0037 or later)** SHALL flip the empty-value handling
  to fail-closed once producer adoption is verified.

## Consequences

### Positive

- Phase 4a path inherits the ADR 0035 invariant. The defence-in-depth
  becomes complete across both approval paths.
- Carry-point discipline mirrors ADR 0027, which the team already knows
  how to reason about and test (preservation tests at each edge).
- Unknown values fail-closed immediately, so the gateway cannot silently
  accept malformed payloads injected via a compromised producer.

### Negative

- Producers must emit `requester_actor_type` to get full coverage. Until
  they do, the gateway's protection on Phase 4a is best-effort
  (`human-operator` fallback). The migration window is intentionally
  short to limit this exposure.
- The `approvalActionValue` JSON shape becomes one field wider. This is
  byte-compatible with existing in-flight buttons because Go's
  `json.Unmarshal` ignores unknown / missing fields by default.

### Neutral

- ADR 0035 is NOT superseded. ADR 0036 extends it. ADR 0035's §Layer 2
  spec ("`ApproveAction` calls validate before auth") stays exact for
  the dispatch path; ADR 0036 specifies the analogous chain for Phase 4a.
- The classifier from Phase A-3 (`ClassifyApproverActorType` +
  `SLACK_AI_AGENT_BOT_USER_IDS`) is reused without modification. The
  Phase 4a path simply gains a second call site for the same function.

## Implementation roadmap (this ADR ships with implementation)

| Phase | Scope | Approx PR |
|---|---|---|
| 0036 implementation | DMail Metadata key constant, `approvalActionValue` field, `buildButtonValues` carry, `handleApprovalAction` validator, ack metadata copy, full preservation test | 1 (this PR) |
| 0036 producer rollout (per tool) | phonewave / sightjack / paintress / amadeus emit `requester_actor_type` | per-tool, separate repos |
| 0036 fail-closed flip | After producer verification, empty value → reject (new ADR) | 1 |
