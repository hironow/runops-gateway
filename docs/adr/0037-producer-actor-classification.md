# 0037. Producer-side actor type classification (extends ADR 0036, refs#0011)

**Date:** 2026-05-08
**Status:** Proposed

## Context

ADR 0036 (Phase 4a approval path actor-type validation, 2026-05-08) extended ADR 0035's invariant to the Phase 4a 4-eyes approval flow with four carry points. ADR 0036 §Migration declared a window (2026-05-08 → 2026-05-31) during which empty / absent `requester_actor_type` is treated as `CallerHumanOperator` for backwards compatibility, with a future ADR to flip the empty-value handling to fail-closed once producer adoption is verified.

ADR 0036 §Migration explicitly marks producer-side rollout as out-of-scope ("ADR 0036 lives in the gateway and only governs gateway-side handling"). However, codex review (gpt-5.5, 2026-05-08 session retrospective) identified a structural gap that must be resolved **before** any producer-side implementation lands:

> "actor type を誰が決めるのか: producer self-declare か、broker-issued identity 由来か"
> "CLI を人間が叩いた場合と AI が CLI を叩いた場合をどう区別するのか"
> "workspace daemon は approval requester として `workspace-daemon` なのか、背後の initiating actor を carry するのか"
> "metadata は信頼境界内の主張なのか、gateway が検証できる claim なのか"
> "一番難しいのは「AI が人間用 CLI を操作した場合」です。プロセスだけ見ると human CLI と区別できない可能性があります。ここを曖昧にすると `requester_actor_type` は security signal ではなく self-attestation になります。"

Without an ADR pinning these axes, the four producer tools (phonewave / sightjack / paintress / amadeus) would each invent their own classification logic, producing per-tool drift and converting `requester_actor_type` into self-attestation that the gateway's ADR 0035/0036 invariant cannot trust.

## Decision

Pin producer-side `requester_actor_type` classification along **four axes**, with `Enforcement inventory` (per the framework codex proposed in 2026-05-08 session retrospective) listed in §Enforcement inventory.

### Axis 1 — Source of truth precedence

Classification source precedence, highest to lowest:

1. **Broker-issued caller-type** (= Token broker per ADR 0032 §grant matrix). When the producer obtained its credential via the broker (= gateway `/broker/token` endpoint), the producer SHALL emit the broker's `caller_type` claim verbatim into `requester_actor_type`. This is the only path the gateway can independently verify.
2. **`RUNOPS_ACTOR_TYPE` env var** (= explicit out-of-band signal). When the producer runs outside the broker path (= local CLI, cdr-job, AI agent runtime that pre-set env), the env var value SHALL be emitted verbatim. Audit log MUST include `actor_type_source=env` so the gateway can distinguish broker-verified from env-asserted.
3. **Tool default** (= per-tool fallback). Each tool SHALL declare a per-tool default `CallerType` in its own ADR (per-tool ADR list in §Per-tool ADR placeholder). Audit log MUST include `actor_type_source=tool_default`.

The gateway's ADR 0036 §Carry point 3 fail-closed handler interprets `actor_type_source != broker` as "self-attestation" and MAY weight downstream policy accordingly. ADR 0036 itself does not currently differentiate; a future ADR (0038 candidate) MAY tighten this.

### Axis 2 — AI driving human CLI distinction

When an AI agent invokes a tool's human-facing CLI (e.g., `paintress run` from a Claude Code subprocess), process introspection alone cannot distinguish from a human invocation. The contract:

- **AI agent runtime SHALL set `RUNOPS_ACTOR_TYPE=ai-agent` before executing any tool CLI.** This is the AI agent's responsibility, not the tool's — the tool cannot detect it via parent process / TTY / `os.Geteuid()` etc., and any heuristic produces false negatives.
- **The tool's default SHALL NOT be `ai-agent`.** Defaulting to `ai-agent` would force every CLI invocation through fail-closed paths once ADR 0036 flips. Defaults are tool-specific (see §Per-tool ADR placeholder) but `ai-agent` is reserved for explicit signal only.
- **Audit log MUST capture the actor_type_source.** A subsequent forensic audit can identify "AI agent forgot to set env" cases by `actor_type=human-operator` + `actor_type_source=tool_default` patterns.

### Axis 3 — Workspace daemon dual-actor carry

A scheduled / triggered workspace daemon (e.g., phonewave courier launched at boot, scheduled cdr-job) is itself a `workspace-daemon` actor, but its actions originate from an upstream actor (the human / AI agent who created the schedule, or the broker that minted the trigger token).

Producers SHALL emit:

- `requester_actor_type` = the **proximate** actor (= the daemon process itself: `workspace-daemon`)
- `initiating_actor_type` = the **distal** actor (= the human / AI agent / gateway-service that scheduled the action), as a separate metadata key

The gateway MAY use `initiating_actor_type` for policy decisions where the proximate actor is uninformative (e.g., "an AI-initiated daemon-driven approval still cannot approve another AI-initiated request"). This carry is non-blocking for ADR 0037 acceptance — implementations MAY omit `initiating_actor_type` initially — but the key SHALL be reserved.

### Axis 4 — Metadata trust boundary

`requester_actor_type` is **producer-attested** at the trust boundary: the gateway receives it but cannot cryptographically verify it without the broker token chain. The interpretation:

- **Broker-verified** (`actor_type_source=broker`): the gateway trusts this for security policy.
- **Self-attested** (`actor_type_source=env|tool_default`): the gateway treats this as a hint; the security policy fail-closed flip (post-2026-06-01) SHALL still apply, but a future ADR (0038 candidate) MAY require broker-verified for `ai-agent` specifically (= "AI agents must obtain broker tokens to be classified as AI agents at all").

This decision intentionally stops short of mandating broker-only paths for all AI agent operations. The reason: many existing flows (= Claude Code local invocations, dev loops) cannot reasonably go through the broker today. Tightening to broker-only `ai-agent` is left as a future ADR contingent on broker rollout coverage.

## Enforcement inventory (per codex framework, 2026-05-08)

Architectural pin ADRs SHALL include this section so that "where can this behavior happen?" is answered before any RED test is written. This ADR's inventory:

### Entry points that emit DMail with `requester_actor_type`

| Tool | Emit site (proposed) | Notes |
|---|---|---|
| phonewave | `internal/courier/dmail_emit.go` | Courier emits convergence DMails it routes; classification is `workspace-daemon` (proximate) |
| sightjack | `internal/dispatcher/emit.go` (or equivalent) | Producer of dispatch / convergence DMails |
| paintress | same | same |
| amadeus | same | same |
| runops-gateway | `internal/adapter/output/pubsub` (DMail publisher) | Emits broker-verified classification when broker token is in context |

### Persistent / carried data needed at each emit site

- broker token claim (`caller_type`) — when available via auth context
- `RUNOPS_ACTOR_TYPE` env var — fallback layer 1
- per-tool default `CallerType` — fallback layer 2
- `actor_type_source` audit attribute — emitted alongside (= breadcrumb for forensic audit)
- `initiating_actor_type` (optional, for daemons) — emitted as separate metadata key

### Bypass candidates (= "where can this go wrong?")

- AI agent runtime forgets to set `RUNOPS_ACTOR_TYPE=ai-agent` → tool defaults apply → potentially classified as `human-operator` → 2026-06-01 flip catches some cases via fail-closed but only when actor_type is explicitly empty; tool defaults will silently mis-classify
- Daemon scheduled an AI-initiated action without `initiating_actor_type` → upstream context lost → AI-vs-AI gate cannot trigger
- Producer emits `ai-agent` self-attested without broker token → gateway cannot verify; this ADR currently allows it (Axis 4) with audit logging, but a future ADR may forbid
- Tool reads stale env var from previous invocation in long-lived process → classification drifts over invocations within the same process; tools SHALL re-read env per emit

### Tests proving coverage (one per enforcement point)

- per-tool: emit DMail with `RUNOPS_ACTOR_TYPE=ai-agent` set → metadata contains `requester_actor_type=ai-agent` + `actor_type_source=env`
- per-tool: emit without env var, broker token absent → tool default + `actor_type_source=tool_default`
- per-tool: emit with broker token (mock broker context) → broker `caller_type` + `actor_type_source=broker`
- gateway: receive metadata with `actor_type_source=env` and `requester_actor_type=ai-agent`, second AI approver clicks → ADR 0035/0036 invariant fires (= integration test layered on top of existing ADR 0036 §Test)

## Migration window alignment

ADR 0036 §Migration declared 2026-05-08 → 2026-05-31 as the window where empty `requester_actor_type` falls back to `human-operator`. ADR 0037 (this ADR) introduces non-empty `requester_actor_type` from producers, so during this window:

- Producers that have rolled out per ADR 0037 emit canonical values
- Producers that have not yet rolled out continue to emit empty (= human fallback per ADR 0036)
- Mix is safe: gateway processes both per ADR 0036 §Carry point 3

ADR 0036's 2026-06-01 fail-closed flip (the future ADR placeholder) SHALL only trigger after producer rollout is observably complete. Acceptance criteria for that flip is not in this ADR; a future ADR (0038 candidate) SHALL define the verification.

## Consequences

### Positive

- The four axes are now decided once, not re-invented per tool, eliminating cross-tool drift in classification semantics.
- `actor_type_source` audit breadcrumb makes self-attested vs broker-verified distinguishable post-hoc, even when the gateway treats both the same in current policy.
- AI-agent classification is opt-in via explicit env (Axis 2), so existing CLIs do not regress to "everything is suddenly AI" classification when this ADR rolls out.
- `initiating_actor_type` reserved key allows future tightening (= "daemon-laundered AI requests cannot bypass the AI-vs-AI invariant").

### Negative

- Producer rollout is per-tool. Until each tool implements ADR 0037, that tool's emissions remain in ADR 0036 migration-window human fallback.
- AI agent runtime contract (Axis 2: "SHALL set RUNOPS_ACTOR_TYPE=ai-agent") is operator-discipline, not gateway-enforced. A forgotten env var produces silent mis-classification that only forensic audit catches.
- `actor_type_source` field broadens the audit log surface; ops dashboards will need to be updated to surface the new dimension.

### Neutral

- ADR 0036 is NOT superseded. ADR 0037 provides the producer-side counterpart of ADR 0036's gateway-side enforcement. The two are paired.
- Per-tool defaults are explicitly delegated to per-tool ADRs (see §Per-tool ADR placeholder). This ADR fixes the framework, not each tool's choice.
- A future ADR (0038 candidate) will revisit (a) broker-only `ai-agent` classification and (b) ADR 0036 fail-closed flip verification criteria. ADR 0037 deliberately does not pin these so the rollout cadence remains flexible.

## Per-tool ADR placeholder

Each producing tool SHALL ship a per-tool ADR after this one is Accepted, declaring:

- the tool's per-tool default `CallerType` (Axis 1.3)
- the tool's emit site path (= row in §Enforcement inventory)
- whether the tool participates in the broker token path (Axis 1.1)
- whether the tool emits `initiating_actor_type` (Axis 3)

| Tool | Per-tool ADR | Status |
|---|---|---|
| phonewave | TBD | not yet drafted |
| sightjack | TBD | not yet drafted |
| paintress | TBD | not yet drafted |
| amadeus | TBD | not yet drafted |
| runops-gateway (when emitting) | folded into ADR 0036 + this ADR | covered |

## Implementation roadmap (out of this ADR)

| Phase | Scope | Approx PR count |
|---|---|---|
| 0037 promote | Proposed → Accepted (this PR is Proposed only; promote is separate per ADR 0035 cadence) | 1 |
| 0037 per-tool ADR | each tool drafts its own ADR pinning Axis 1.3 default + emit site + broker participation | 4 |
| 0037 per-tool implementation | each tool implements emit + tests per §Enforcement inventory | 4 (one per tool, possibly split phase) |
| 0038 candidate (fail-closed flip + broker-only `ai-agent`) | revisit ADR 0036 migration end-state + broker-only AI classification | 1 (future, contingent on rollout coverage) |

## Refs

- ADR 0035 (architectural pin: AI vs AI invariant)
- ADR 0036 (Phase 4a path extension + 4 carry points + migration window)
- ADR 0019 (4-eyes approval, narrowed by 0035)
- ADR 0032 (broker grant matrix, 4 caller-type taxonomy)
- ADR 0027 (carry point discipline reference)
- codex (gpt-5.5) session retrospective 2026-05-08 (root cause: enforcement point inventory absence; framework: §Enforcement inventory section)
- refs#0011 (cross-repo issue this ADR completes the closing bracket of)
