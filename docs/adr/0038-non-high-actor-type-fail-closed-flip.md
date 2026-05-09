# 0038. Non-HIGH path actor-type fail-closed flip (refs#0011)

**Date:** 2026-05-09
**Status:** Proposed

## Context

ADR 0036 (Phase 4a approval actor validation, 2026-05-08) §Migration declared a window 2026-05-08 → 2026-05-31 during which an empty / absent `requester_actor_type` is treated as `CallerHumanOperator` so the gateway stays compatible with producers that have not yet emitted the key. The same §Migration paragraph predicts a follow-up ADR:

> A future ADR (0037 or later) SHALL flip the empty-value handling to fail-closed once producer adoption is verified.

ADR 0037 (producer-side actor classification, 2026-05-08) §Migration window alignment narrowed that allowance: empty / `unknown` is fail-closed for HIGH severity 4-eyes paths from ADR 0037 acceptance day, while non-HIGH paths (= dispatch / canary deploy via `ApproveAction`) keep the migration-window `CallerHumanOperator` fallback until a future ADR.

The producer-side rollout completed on 2026-05-09 (refs issue 0018):

- sightjack PR #205 → `f158395` main merged
- paintress PR #207 → `8ad49c3` main merged
- amadeus PR #208 → `a47a6cf` main merged
- phonewave PR #143 → `243071f` main merged (relay-preserve, no emit)
- dominator PR #21 → `62f3205` main merged
- dotfiles ADR 0012 + Path A/B/C/D wrapper PR #96 / #97 → `efa5c50` / `09f391b` main merged

The four **emit-side** producer tools (sj/pt/am/dom) now emit `metadata.requester_actor_type` when `RUNOPS_ACTOR_TYPE` env is set; the **relay-only** producer (phonewave) carries the metadata byte-for-byte without emit per phonewave ADR 0005, satisfying its rollout gate by preservation. The dotfiles-side `RUNOPS_ACTOR_TYPE` injection wrapper covers the four caller paths (workspace-daemon / cdr-job / cdr-exec / Claude Code `cc*` aliases). Producer adoption from a system-state perspective is **complete** for both classes (= 4 emit-tools all set the env via dotfiles wrapper; 1 relay-tool preserves the carry).

What remains before flipping the non-HIGH fail-open is **observed adoption**, not declared adoption. ADR 0036 §Migration scheduled `slog.Warn("approval_actor_type_missing", ...)` to begin emitting from 2026-06-01. The flip is conditional on the volume of that warning hitting and staying at zero across a representative window. This ADR pins the flip design and the trigger gates, but the Accepted promotion happens only when the gates are crossed.

## Decision

The non-HIGH severity actor-type empty-value handling (governed by `ApproveAction` per ADR 0035 §Layer 2 and broker / dispatch entry points enumerated in ADR 0032) flips from `CallerHumanOperator` fallback to **fail-closed reject** when the trigger conditions are met. HIGH severity remains fail-closed (no change — ADR 0037 already pinned this path).

### 1. Behavior change — non-HIGH paths only, scoped to use-case layer

Today (= ADR 0036 §Migration window applied to non-HIGH dispatch / canary):

```
RequesterActorType="" → ApproveAction calls ValidateApproverPermitted
                      → domain validator treats empty as CallerHumanOperator
                      → ApproveAction proceeds
```

After flip (= ADR 0038 Accepted):

```
RequesterActorType="" → ApproveAction's pre-validate empty check
                      → return ErrEmptyRequesterActorType (sentinel) before reaching domain validator
                      → ApproveAction rejects (no dispatch)
```

The reject is added **only inside `ApproveAction`** (= use-case layer, non-HIGH dispatch / canary path). The shared `ValidateApproverPermitted` domain function is **not** modified — its `CallerHumanOperator` fallback for empty values remains, because that fallback also serves any future non-Approve callsite that explicitly opts into the legacy semantic. Scoping the change to `ApproveAction` keeps the flip non-HIGH-only and allows the rollback in §Rollback to be a code-level revert of the use-case branch.

Sentinel name follows ADR 0036 §Carry point 3 convention (`domain.ErrAIAgentCannotApproveAIAgent` precedent). Proposed: `domain.ErrEmptyRequesterActorType`. The error wraps an audit log line `slog.Warn("approval_actor_type_empty_rejected", ...)` so observability gets a clear marker for the flip moment.

HIGH severity 4-eyes approval (= Phase 4a) is **untouched by this ADR**: ADR 0036 + ADR 0037 §Migration window alignment specify HIGH as fail-closed at the **spec level** (= the `unknown` classification in ADR 0037 §Gateway policy table), but the current Slack interactive handler implementation (`internal/adapter/input/slack/handler.go::handleApprovalAction`, plus `handler_test.go`'s `TestInteractiveHandler_ApprovalApprove_LegacyEmpty_Publishes` regression fixture) still publishes when `RequesterActorType` is empty. Reconciling that spec-vs-impl gap is the responsibility of an ADR 0037 follow-up implementation PR, **not** this ADR. ADR 0038 explicitly does not pretend the HIGH path is implemented-fail-closed today, and §3 below adds a trigger condition that this gap is closed before ADR 0038 can flip.

ADR 0038 only changes the `ApproveAction` non-HIGH dispatch / canary path. The HIGH-path implementation alignment is named §3.4 below as an explicit gate so the two flips do not race.

### 2. Code change locations

| Layer | File | Today | After flip |
|---|---|---|---|
| use-case | `internal/usecase/runops.go` `ApproveAction` (= ADR 0035 Layer 2 dispatch / canary) | calls `ValidateApproverPermitted`; empty requester is treated as `CallerHumanOperator` and `ApproveAction` proceeds | adds an explicit empty check at the top of `ApproveAction` that returns `domain.ErrEmptyRequesterActorType` before invoking the domain validator |
| domain (sentinel) | `internal/core/domain/...` (next-to existing `ErrAIAgentCannotApproveAIAgent`) | not present | new `ErrEmptyRequesterActorType` sentinel value |
| domain (validator, **NO change**) | `internal/core/domain/approver_validator.go` (`ValidateApproverPermitted`) | empty `RequesterActorType` → `CallerHumanOperator` fallback | unchanged (used by any callsite that explicitly opts into the legacy semantic; `ApproveAction` no longer reaches it on empty) |
| domain comment sweep | `internal/core/port/port.go:18` + `internal/core/domain/dmail.go:75` | "treated as CallerHumanOperator during the migration window (see ADR 0036)" | append "post ADR 0038 flip, `ApproveAction` rejects empty for non-HIGH dispatch" — describe both behaviors so the comment reflects the actual state of the codebase |
| use-case test | `internal/usecase/runops_test.go` (or equivalent location for `ApproveAction` tests) | empty requester rows expect dispatch to proceed | empty requester rows expect `ErrEmptyRequesterActorType` sentinel; well-formed actor-type rows continue to dispatch |
| domain validator test | `internal/core/domain/approver_validator_test.go` | empty / human-requester rows assume fallback | **unchanged** — the domain validator's fallback semantic stays, so its tests stay |
| Slack inbound (Phase 4a, **NOT touched by ADR 0038**) | `internal/adapter/input/slack/handler.go` `handleApprovalAction` | spec-level fail-closed for empty (ADR 0036 + 0037 §Migration window alignment) **but implementation currently publishes legacy empty** (= `TestInteractiveHandler_ApprovalApprove_LegacyEmpty_Publishes` documents this state) | ADR 0037 follow-up PR (= ADR 0038 §3.4 + Migration Phase 0.5) brings handler.go to spec-aligned fail-closed before ADR 0038 evaluates §3.1-§3.3. ADR 0038 itself makes no edit to this file |

The change is intentionally small: one sentinel + one early-return branch in `ApproveAction` + comment / use-case test sweep. The shared domain validator is left alone, so any future callsite that explicitly wants the legacy fallback can still call `ValidateApproverPermitted` directly.

### 3. Migration trigger conditions

The Accepted promotion is gated on **all** of the following:

0. **`approval_actor_type_missing` warn log is implemented, deployed to production, and verified searchable in the log infrastructure.** ADR 0036 §Migration scheduled this warn for 2026-06-01 but did not pin the implementation PR. Without §3.0 satisfied, "zero warn count" is indistinguishable from "no telemetry path". §3.0 requires (a) the warn-log code shipped, (b) the production deploy reached, (c) at least one synthetic empty-requester invocation triggers a visible log line that the operator can grep in the production log infrastructure (Cloud Logging / equivalent). Without all three of (a)/(b)/(c), §3.2's 14-day window cannot start.
1. **Producer rollout main-merged** for all producers — emit-side (sj/pt/am/dom) + relay-only side (pw) + dotfiles wrapper. ✅ Met 2026-05-09.
2. **`approval_actor_type_missing` warn log emits at zero** for at least 14 consecutive days starting on or after the later of (a) 2026-06-01 and (b) the §3.0 verification date. The 14-day window must cover both weekday and weekend traffic shapes; if any non-zero warn fires, the count restarts.
3. **`approval_actor_type_empty_rejected` shadow log added before flip** (= one PR ahead of this ADR's Accepted promotion) so the flip moment is observable. The shadow log is defensive: it lets the operator confirm the flip's behavior in a single grep before / after deployment, and is independent of the §3.0 / §3.2 warn signal so a single-source telemetry bug does not falsify the 14-day zero observation.
4. **HIGH path spec-vs-impl gap closed (ADR 0037 follow-up)**. Today the Slack interactive handler `handleApprovalAction` still publishes when `RequesterActorType` is empty (= `TestInteractiveHandler_ApprovalApprove_LegacyEmpty_Publishes` documents this fixture). ADR 0037 §Gateway policy table treats `unknown` (= empty source) as fail-closed for HIGH at the spec level, but the implementation has not caught up. ADR 0038 cannot flip the non-HIGH path while the HIGH path still accepts empty in production, because operators reading the audit trail would conclude both severities tolerate empty. §3.4 requires a separate ADR 0037 follow-up PR (= handler.go aligns with the spec; the legacy-empty publish fixture flips to a fail-closed assertion) to land before §3 evaluation begins.

When all five are satisfied, this ADR moves Proposed → Accepted in a separate PR, and the flip implementation lands in another PR or in the same Accepted-promotion PR (operator's call). Both ADR 0035 and ADR 0036 use the "Accepted ships with implementation" pattern; ADR 0038 follows the same pattern for the implementation PR but the trigger gates make a single combined PR awkward, so the Accepted-promotion PR is allowed to be small (status flip + comment sweep) with a follow-up implementation PR if telemetry deteriorates between rounds.

### 4. Rollback semantics

If the flip causes a production incident (= unexpected non-HIGH dispatches blocked because a hidden producer surface was missed), revert by:

1. Reverting the `ApproveAction` branch and the sentinel introduction in a hotfix PR.
2. Adding the offending producer surface to refs issue 0018's checklist as a follow-up rollout entry.
3. Re-running the 14-day telemetry window before re-attempting the flip.

The gateway never deletes the migration-window `CallerHumanOperator` fallback **logic** — only the dispatch path that uses it. This makes rollback a code-level revert rather than a data-shape change.

## Enforcement inventory

This ADR pins a system-level invariant transition (= empty actor-type can never be accepted on the non-HIGH path after the flip date). Per the .claude/CLAUDE.md ADR template requirement:

### Entry points

- `internal/usecase/runops.go` `ApproveAction` (= ADR 0035 Layer 2, the dispatch / canary approval gate; this is the only enforcement entry point that this ADR modifies)
- `internal/adapter/input/slack/handler.go` `handleApprovalAction` (= HIGH Phase 4a path per ADR 0036; ADR 0037 follow-up PR (§3.4 of this ADR) brings this site to spec-aligned fail-closed BEFORE ADR 0038 evaluates §3.1-§3.3, so by the time ADR 0038 is Accepted both ApproveAction and handleApprovalAction reject empty)
- **All non-HIGH approval / dispatch entry points MUST go through `ApproveAction` or implement an equivalent empty-check gate.** A new use-case caller that bypasses `ApproveAction` and calls `ValidateApproverPermitted` directly is a structural bypass of this ADR's invariant and MUST NOT be added without a superseding ADR. The shared `ValidateApproverPermitted` validator's legacy `CallerHumanOperator` fallback exists for **non-approval** uses only (= e.g., audit-only inspection callsites); using it as a substitute for `ApproveAction`'s empty-check is forbidden

### Persistent / carried data needed at each enforcement point

- `requester_actor_type` ∈ {`human-operator`, `gateway-service`, `ai-agent`, `workspace-daemon`} (4 canonical values, exact match)
- Empty / absent `requester_actor_type` = the new fail-closed condition for non-HIGH paths
- `RequesterActorType` field on `domain.ApprovalRequest` (introduced PR #91, ADR 0035 Phase A-1)
- `metadata.requester_actor_type` carried by D-Mail (ADR 0036 §Carry point 3)

### Bypass candidates

- A producer that emits the legacy frontmatter shape (= without `requester_actor_type`) → fail-closed by design after flip; this is the intent
- A request that bypasses `ApproveAction` and reaches `ValidateApproverPermitted` directly (= e.g., a future RPC endpoint added without re-reading this ADR) → **structurally forbidden** by the Entry points clause above; introducing such a callsite for an approval / dispatch use is a superseding-ADR-required change, not an in-scope variation. As an optional defense-in-depth, a future semgrep rule (= flag any non-test caller of `ValidateApproverPermitted` outside `internal/usecase/runops.go`) MAY be added in a separate PR; this ADR does not introduce that rule itself but explicitly invites it
- `RequesterActorType` field added to a NEW approval shape (e.g., a Phase 4b path created later) without re-reading ADR 0035-0038 → mitigated by the .claude/CLAUDE.md ADR template §Enforcement inventory framework that future ADRs must list their own entry points
- Telemetry-only signal misread as "zero adoption" when in fact warn log infrastructure was broken → mitigated by trigger condition 3 (= shadow log `approval_actor_type_empty_rejected` added BEFORE flip), which both observes the flip and surfaces a wiring failure independently from the warn log
- Migration-window code path left around for a future "v3 fallback" → explicitly forbidden in §Rollback semantics (revert is code-level, not data-shape)

### Tests proving coverage

| Test | Layer | Verifies |
|---|---|---|
| `TestApproveAction_EmptyRequesterActorType_FailsClosed` | use-case (`internal/usecase/runops_test.go` or equivalent) | dispatch / canary call to `ApproveAction` with `RequesterActorType=""` returns `ErrEmptyRequesterActorType` and never invokes the dispatch |
| `TestApproveAction_KnownActorTypes_StillDispatch` | use-case | well-formed (4 canonical) `RequesterActorType` values still proceed to dispatch (= no regression) |
| `TestApproverValidator_EmptyRequester_StillFallsBackToHumanOperator` | domain (`approver_validator_test.go`) | direct call to `ValidateApproverPermitted` retains the legacy fallback (= ADR 0038 does NOT change the shared validator) |
| `TestApproverValidator_KnownActorTypes_StillPass` | domain | the 13-row matrix from ADR 0035 PR #91 stays green (= no regression on the well-formed paths) |
| `TestSlackHandleApprovalAction_EmptyRequesterActorType_FailsClosed` | inbound (`internal/adapter/input/slack/handler_test.go`) | Phase 4a HIGH path stays fail-closed for empty (regression assertion against ADR 0036 / ADR 0037; this ADR does NOT touch this path but tests it to detect any silent breakage) |
| `TestApproveAction_EmptyRequesterActorType_ShadowLogEmits_PreFlip` | use-case (shadow phase, optional) | before Accepted, the shadow log `approval_actor_type_empty_rejected` fires on empty without rejecting; once §3.2 zero observed, this test is updated to also assert the sentinel return |

## Migration

The implementation phases:

| Phase | Scope | Trigger | Approx PR |
|---|---|---|---|
| **0 (Proposed, this PR)** | ADR draft + spec lock-in | none — design only | this PR (status: Proposed, develop merge ok per gateway branch policy) |
| **0.5 (HIGH path spec alignment)** | ADR 0037 follow-up PR: bring `handleApprovalAction` to fail-closed on empty `RequesterActorType` (= flip `TestInteractiveHandler_ApprovalApprove_LegacyEmpty_Publishes` to a reject assertion) | none — this is an ADR 0037 implementation gap that exists today regardless of ADR 0038 | separate ADR 0037 follow-up gateway PR (this ADR §3.4 depends on it landing) |
| **1 (warn-log + shadow phase)** | Implement `approval_actor_type_missing` warn (§3.0) + add `approval_actor_type_empty_rejected` shadow log (§3.3); both are observe-only, no behavior change | can ship after Phase 0 lands; Phase 0.5 not strictly required to start Phase 1 | follow-up gateway PR |
| **2 (Accepted promotion)** | ADR status flip Proposed → Accepted + sentinel `domain.ErrEmptyRequesterActorType` + `ApproveAction` empty-check early-return | trigger conditions **§3.0 + §3.1 + §3.2 + §3.3 + §3.4 all met** | follow-up gateway PR |
| **3 (cleanup)** | Sweep stale comments referencing the migration-window fallback (port.go:18, dmail.go:75, etc.) and update ADR 0037 §Migration window alignment paragraph to record that the asymmetry is now closed | landed alongside Phase 2 or as a separate `chore(docs)` PR | follow-up gateway PR |

If the 14-day telemetry zero window does not close cleanly (e.g., a forgotten producer surface emits empties), Phase 2 / 3 wait. Phase 1 (shadow log) keeps producing observability data without blocking traffic, so the team can investigate without rolling back any user-facing change.

## Consequences

### Positive

- The producer-side rollout's correctness benefit becomes binding rather than advisory. AI dispatches that forget `RUNOPS_ACTOR_TYPE` no longer launder to `human-operator`; the gateway visibly rejects, the operator fixes the env wiring at the offending caller, and the audit trail stays faithful.
- Gateway ADR 0035-0037's invariants converge across HIGH and non-HIGH severities. The asymmetry that ADR 0037 §Migration window alignment introduced as a deliberate temporary measure is dissolved.
- The shadow-log phase (= Phase 1) gives the operator a ground-truth observability surface independent of the existing `approval_actor_type_missing` warn log. Two independent signals make the flip's "telemetry zero" trigger robust against single-source bugs.

### Negative

- A forgotten producer surface (= a code path that has not yet been migrated to read `RUNOPS_ACTOR_TYPE`) becomes a hard-fail after the flip. This is the intended fail-closed semantic, but it implies an operator burden during the 14-day shadow phase to run down every empty fire.
- Test fixture sweep (= `approver_validator_test.go` and friends) lands as part of the Accepted-promotion PR. The test fixture has assumed `CallerHumanOperator` fallback semantics for non-HIGH since ADR 0035 Phase A-1 (PR #91); the sweep is mechanical but touches multiple files.
- An incident in the first hours after flip is plausible: the producer rollout was completed in a single same-day batch on 2026-05-09, so any environmental skew between dev/prod will surface here. The shadow log gives early warning, but the on-call needs to be primed.

### Neutral

- ADR 0036 is not superseded. ADR 0036 §Migration explicitly named "a future ADR (0037 or later)" as the flip vehicle; ADR 0038 fulfills that promise without invalidating ADR 0036's structure.
- ADR 0037 §Migration window alignment becomes historical: from Accepted day, both HIGH and non-HIGH paths fail-closed on empty. The §Migration window alignment paragraph stays in ADR 0037 as the audit trail of why the asymmetry existed.
- The 4-canonical-value taxonomy (ADR 0032) is unchanged. ADR 0038 only changes how `""` is interpreted on the non-HIGH path; the four canonical values continue to validate as before.

## References

- ADR 0019 — original 4-eyes approval gate
- ADR 0032 — token-broker caller grant matrix (4 caller types)
- ADR 0035 — AI agent cannot approve AI agent (architectural pin)
- ADR 0036 — Phase 4a approval actor validation (effective rule + §Migration that predicts this ADR)
- ADR 0037 — producer-side actor classification (§Migration window alignment narrows ADR 0036 for HIGH paths)
- refs/docs/issues/0011-runops-gateway-ai-agent-identity-4-eyes — parent issue
- refs/docs/issues/0018 — fix-up tracker (= 0011 producer-side rollout 完遂)
- sightjack docs/adr/0017 — producer-side spec (4-producer common)
- phonewave docs/adr/0005 — relay-preserve invariant
- dotfiles docs/adr/0012 — workspace VM env injection per caller path
