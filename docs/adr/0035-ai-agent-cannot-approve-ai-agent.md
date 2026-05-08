# 0035. AI agent cannot approve AI agent (refs#0011)

**Date:** 2026-05-08
**Status:** Proposed

## Context

ADR 0019 (Phase 4a 4-eyes approval gate, 2026-05-05) introduced the
HIGH-severity convergence approval flow: a high-impact convergence
D-Mail is held in Slack until a second human reviews + clicks
Approve. The original implementation enforces the
"approver != original_requester" predicate at the Slack
InteractiveHandler layer (`approval_approve` action), so a single
human cannot self-approve.

Two facts that the original ADR 0019 design did not fully address:

1. **The "actor" was implicitly assumed to be human.** Both the
   original requester and the approver are identified by Slack
   `user.ID`; whether the user is a human or a bot was not
   distinguished. In 2026-05, no production code path emitted an
   approval action from anything other than a human Slack user, so
   the gap was latent.
2. **Token broker (refs#0007, 2026-05-08, runops-gateway PR #53-#83)
   shipped the AI-agent caller path.** The
   `DelegatedAgentVerifier` (PR #63) means the gateway can now
   programmatically distinguish an AI agent from a human at the
   *token-mint* surface. By symmetry, an AI agent that drives a
   workspace daemon could in principle produce Slack interactive
   payloads on behalf of itself or another AI agent — the moment
   such a code path is added, the existing 4-eyes guard would
   accept it because it only checks user-id inequality.

The "AI agent cannot approve AI agent" invariant — pinned in
plan v8 §5.1 ("Slack approval と同じく『人間の指示と AI の指示を
区別しない』 原則") and implicitly assumed by the broker grant
matrix (ADR 0032) — needs an explicit policy + structural
enforcement before any AI-agent-side approval code is added.

## Decision

Pin the invariant **"AI agent cannot approve another AI agent's
convergence request"** as a structural rule, enforced at three
layers (defence-in-depth, mirroring the broker grant matrix's
3-layer enforcement from ADR 0032):

### Layer 1 — Domain model

`domain.ApprovalRequest` gains a `RequesterActorType` field of
type `domain.CallerType` (the existing 4-caller-type enum from
PR #56). Zero value (= empty string) is treated as
`CallerHumanOperator` so backwards compatibility with the 49
existing construction sites is preserved without per-site edits.

A second field `ApproverActorType` is set by the inbound Slack
handler at action time; it is NOT persisted on the original
`ApprovalRequest`.

A pure validation function:

```go
func ValidateApproverPermitted(req ApprovalRequest, approverType CallerType) error
```

returns `ErrAIAgentCannotApproveAIAgent` when both
`req.RequesterActorType == CallerAIAgent` AND
`approverType == CallerAIAgent`.

### Layer 2 — Use-case orchestration

`usecase.RunOpsService.ApproveAction` calls
`ValidateApproverPermitted` BEFORE `auth.IsAuthorized` so the
AI-vs-AI rejection produces a precise audit signal even when the
approver would otherwise be a known operator.

### Layer 3 — Slack inbound adapter

`InteractiveHandler` resolves the `approver` actor type from a
configured Slack bot-user allow-list (env
`SLACK_AI_AGENT_BOT_USER_IDS`, CSV). Any Slack `user.id` whose
prefix matches the bot pattern OR whose id appears in the env
list is classified as `CallerAIAgent`; otherwise
`CallerHumanOperator`. The original requester's actor type is
read from the existing D-Mail / dispatch metadata that the
convergence request carries.

The Slack handler hands the resolved actor type to the use case;
the use case rejects with HTTP 403 + `audit_event=ai_approves_ai_attempt`.

## Consequences

### Positive

- Structural ban on the AI-vs-AI approval path. A future code
  change that would route an AI agent's approval click into
  `usecase.ApproveAction` is blocked by the domain validator
  rather than relying on a hand-written "is this a bot?" check
  per call site.
- Audit log distinguishes "human approver but unauthorised"
  (existing `auth.IsAuthorized` rejection) from "AI approver
  attempting AI approval" (new sentinel).
- The 49 existing `ApprovalRequest{...}` construction sites are
  not changed — backwards compat is preserved by zero-value
  default semantics.

### Negative

- A bot-user allow-list (`SLACK_AI_AGENT_BOT_USER_IDS`) is a new
  ops surface. Empty list = no AI agent can be in approver role
  for the policy to even fire (= safe default; rule is a no-op
  until a bot user is enrolled).
- The "actor type" inference for the original requester depends
  on the convergence request's metadata carrying that information
  through the dmail → dispatch → approval pipeline. Existing
  metadata likely needs an additional field; that lift lands
  with the implementation PR sequence, not this ADR.

### Neutral

- ADR 0019 (4-eyes approval) is NOT superseded — its
  "approver != original_requester" predicate stays. ADR 0035
  layers on top, narrowing the approver-type when both sides are
  AI.
- 0011 (the cross-repo issue this ADR resolves) gets its
  architectural pin via this ADR. Implementation lands as a
  separate PR sequence after this ADR is Accepted, mirroring
  the ADR 0034 → ADR-Accepted → workflow-implementation cadence
  used in 2026-05-08 PR #84/#88.

## Implementation roadmap (out of this ADR)

| Phase | Scope | Approx PR count |
|---|---|---|
| 0035 Phase A-1 | `domain.ApprovalRequest` field extension + `ValidateApproverPermitted` + sentinel | 1 |
| 0035 Phase A-2 | `usecase.RunOpsService.ApproveAction` integration + audit log | 1 |
| 0035 Phase A-3 | Slack `InteractiveHandler` actor-type resolution + env var | 1 |
| 0035 Phase A-4 | Convergence request metadata extension (RequesterActorType through dmail / dispatch / approval pipeline) | 1-2 |
| 0035 Phase A-5 | paths.yaml glob for `docs/adr/0035-*` (auth_boundary classification) | 0 (folded into A-1 PR) |

Each phase lands as its own PR with the new sentinel covered by
a unit test that asserts `errors.Is(err, ErrAIAgentCannotApproveAIAgent)`.
