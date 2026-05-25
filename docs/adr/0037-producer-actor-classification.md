# 0037. Producer-side actor type classification (extends ADR 0036, refs#0011)

**Date:** 2026-05-08
**Status:** Accepted

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

### Axis 1 — Source of truth (no silent fallback)

Producers MAY emit `requester_actor_type` only when one of the two sources below provides it. **There is no `tool_default` fallback** — this is intentional, see §Why tool_default is forbidden below.

1. **Broker-issued caller-type** (= Token broker per ADR 0032 §grant matrix). When the producer's request flows through `/broker/token` and the broker's `caller_type` claim is available in the request context, the **gateway** itself attaches `actor_type_source=broker` (only the gateway can do this — see Axis 4). Producer-side `actor_type_source=broker` is rejected.
2. **`RUNOPS_ACTOR_TYPE` env var** (= explicit out-of-band signal). When the producer runs outside the broker path and the env var is set to a canonical CallerType string, the producer emits the value verbatim with `actor_type_source=env`. Empty / unset env yields **no emission at all** for `requester_actor_type`.

When neither source provides a value, the producer MUST emit `requester_actor_type` as **empty** (= the field is set to `""` or omitted from metadata). This routes the message into ADR 0036 §Carry point 3's empty-handling path — currently human-operator fallback during the migration window, fail-closed after the 2026-06-01 flip.

### Why tool_default is forbidden

A per-tool default (e.g., `human-operator` for sightjack producer, set when neither broker nor env provides a value) would defeat ADR 0036's fail-closed flip:

```text
AI agent invokes paintress CLI without RUNOPS_ACTOR_TYPE
→ producer emits tool_default = "human-operator"
→ requester_actor_type is non-empty
→ ADR 0036's empty-fail-closed flip never triggers
→ AI request approved as a human request (silent mis-classification)
```

This was a codex (gpt-5.5) finding on the pre-rewrite v1 of this ADR. The fix is not "audit log + downstream weighting", because audit log catches the bypass after the breach. The fix is to **never let the producer emit a non-empty value the producer cannot justify**.

### Axis 2 — AI driving human CLI distinction

When an AI agent invokes a tool's human-facing CLI (e.g., `paintress run` from a Claude Code subprocess), process introspection alone cannot distinguish from a human invocation. The contract:

- **AI agent runtime SHALL set `RUNOPS_ACTOR_TYPE=ai-agent` before executing any tool CLI.** This is the AI agent's responsibility, not the tool's — the tool cannot detect it via parent process / TTY / `os.Geteuid()` etc., and any heuristic produces false negatives.
- **A forgotten env var produces empty `requester_actor_type`, not a default.** Per Axis 1, no tool_default exists. Empty propagates to ADR 0036's empty-handling, which (post-2026-06-01) is fail-closed for HIGH severity approval paths.
- **The tool MUST never write `ai-agent` on its own** under any circumstance. `ai-agent` only appears via env (Axis 1.2) or via gateway-attached broker source (Axis 1.1). Producers writing `ai-agent` from heuristics are explicitly forbidden.

### Axis 3 — Workspace daemon dual-actor carry (now mandatory for HIGH)

A scheduled / triggered workspace daemon (e.g., phonewave courier launched at boot, scheduled cdr-job) is itself a `workspace-daemon` actor, but its actions originate from an upstream actor (the human / AI agent who created the schedule, or the broker that minted the trigger token).

Producers emit:

- `requester_actor_type` = the **proximate** actor (= the daemon process itself: `workspace-daemon`)
- `initiating_actor_type` = the **distal** actor (= the human / AI agent / gateway-service that scheduled the action), as a separate metadata key

#### How `workspace-daemon` enters the producer (Axis 1 reconciliation)

Axis 1 limits sources of `requester_actor_type` to two — broker (gateway-attached) and `RUNOPS_ACTOR_TYPE` env (producer-emittable). A daemon is no exception. The daemon does **not** self-classify by inspecting its own process role; that would be Axis 1's forbidden self-attestation path. Instead:

- The supervisor that starts the daemon (= systemd unit, scheduled launcher, cdr-job runner) SHALL set `RUNOPS_ACTOR_TYPE=workspace-daemon` in the daemon process's environment **before exec**.
- The daemon, when emitting a DMail, reads the env exactly like Axis 1.2 says, and emits `requester_actor_type=workspace-daemon` with `actor_type_source=env`.
- A daemon process whose supervisor neglected to set the env emits empty `requester_actor_type` (= `actor_type_source=unknown`), exactly like a forgetting AI agent. The gateway handles per the §Migration window alignment policy and §Gateway policy table.

Per-tool ADRs MUST document the supervisor unit / launcher path that sets the env, so that "the daemon classifies itself" never sneaks in as a substitute. There is no special daemon source vocabulary — `workspace-daemon` is just one of the canonical CallerType values that producers may emit via env.

#### `initiating_actor_type` as the laundering closure

For HIGH severity convergence DMails, **`initiating_actor_type` is REQUIRED when `requester_actor_type=workspace-daemon`**. The gateway rejects (= fail-closed at the ADR 0036 §Carry point 3 layer) when `requester_actor_type=workspace-daemon` AND `initiating_actor_type` is missing on a HIGH severity approval flow.

This closes the laundering path codex (gpt-5.5) identified:

```text
AI agent → schedules / triggers daemon
→ daemon emits requester_actor_type=workspace-daemon, initiating_actor_type omitted
→ AI-vs-AI gate sees requester != ai-agent → invariant bypassed
```

`initiating_actor_type` source rules mirror Axis 1: the supervisor that launches the daemon for a given action SHALL also set a second env var (proposed name: `RUNOPS_INITIATING_ACTOR_TYPE`) when the action is scheduled by a non-default actor, OR the broker context (when the daemon is broker-driven) attaches it via gateway-internal carriage. Producers do not infer the distal actor.

For non-HIGH paths (= dispatch / canary deploy where ADR 0035 §Layer 2 governs via `ApproveAction`), `initiating_actor_type` remains optional. The mandatory boundary aligns with ADR 0036's HIGH severity scope.

#### `effective_requester_actor_type` — carry is necessary but not sufficient

Carrying `initiating_actor_type` only protects against laundering if the gateway **uses** it in the AI-vs-AI determination. Codex (gpt-5.5) v3 review flagged that v3 specified the carry but not the use, leaving this bypass open:

```text
AI agent → schedules / triggers daemon
→ daemon emits requester_actor_type=workspace-daemon, initiating_actor_type=ai-agent
→ AI approver clicks
→ ValidateApproverPermitted receives requester_actor_type=workspace-daemon
→ AI-vs-AI invariant does NOT fire → bypass via "I carry the truth but never look at it"
```

For HIGH severity Phase 4a approval, the gateway SHALL compute `effective_requester_actor_type` per the rule below and pass that value (not the raw `requester_actor_type`) to `ValidateApproverPermitted`:

```text
if requester_actor_type == workspace-daemon:
    if initiating_actor_type is empty: fail-closed (per the laundering closure above)
    effective_requester_actor_type = initiating_actor_type
else:
    effective_requester_actor_type = requester_actor_type

handleApprovalAction calls ValidateApproverPermitted(effective_requester_actor_type, approverActorType).
```

Two consequences:

- An AI-initiated daemon-driven action (= `requester_actor_type=workspace-daemon`, `initiating_actor_type=ai-agent`) clicked by an AI approver fires `ErrAIAgentCannotApproveAIAgent`. The daemon hop is no longer a laundering path.
- A human-initiated daemon-driven action (= `initiating_actor_type=human-operator`) clicked by an AI approver passes the AI-vs-AI gate (because `effective_requester == human-operator`), exactly as it would if the human had clicked Approve directly.

This rule applies only on Phase 4a HIGH severity paths. Dispatch / canary deploy paths (ADR 0035 §Layer 2 via `ApproveAction`) continue to use the raw `requester_actor_type` because their flows do not currently surface a `workspace-daemon` requester (= `ApproveAction` is invoked from CLI / Slack interactive, not from a daemon-driven inbox path). A future ADR MAY extend `effective_requester_actor_type` to those paths if a daemon-driven dispatch flow is added.

### Axis 4 — Two enums: metadata input vs gateway-internal classification

Codex (gpt-5.5) v2 review flagged that mixing producer-writable values and gateway-derived values in a single `actor_type_source` enum invites implementer confusion (= "is `self_attested_broker_claim` something a producer can write?"). v3 splits the concept into two enums:

#### Metadata input enum (= what producers may write)

Closed enum: `{ broker, env, unknown }`.

- **`broker`**: producer attempted to write `broker`. Producers SHOULD NOT emit this — only the gateway-internal emit path (gateway-side composition) sets `broker`. If a downstream producer emits it, the gateway treats it as a spoof attempt at the classification step (see below).
- **`env`**: producer set the value because `RUNOPS_ACTOR_TYPE` provided it (Axis 1.2). This is the canonical producer-side source.
- **`unknown`**: producer had neither broker context nor env. The field is set to `unknown` (or the entire `actor_type_source` key may be absent — both are equivalent and treated as `unknown` by the gateway).

#### Gateway-internal classification enum (= how the gateway interprets what arrived)

Closed enum: `{ broker_verified, env_attested, unknown, spoofed_broker }`.

The gateway derives one of these from the arriving DMail metadata, the request context (= whether the inbound DMail came from a path the gateway itself authenticated), and the carry rules:

- **`broker_verified`**: gateway-internal emit path (= the gateway itself wrote the metadata using its own broker context). Only this classification carries the broker-verified trust weight.
- **`env_attested`**: producer-emitted `actor_type_source=env`. Self-attested but acknowledged.
- **`unknown`**: producer-emitted `unknown` or absent. Unverified.
- **`spoofed_broker`**: producer-emitted `actor_type_source=broker` from a path the gateway cannot verify (= producer is asserting broker provenance the gateway did not attach). Treated as a spoof attempt: fail-closed + audit log `actor_type_source_spoof_attempt`.

Implementations MUST keep the two enums distinct (separate Go types in the gateway codebase, separate JSON keys if both ever appear in audit). v3 deliberately makes the input enum smaller than the classification enum so that adding a new gateway-internal interpretation later does not force a producer-side schema change.

This ADR only governs the metadata input. The gateway-internal classification is implementation-defined; the table above is the **default mapping**, and a future ADR MAY refine it (e.g., adding nuance for `env_attested` with `ai-agent` value to require broker provenance) without forcing a producer rewrite.

### Gateway policy on classification (decided in this ADR, NOT deferred to 0038)

For HIGH severity convergence approval paths governed by ADR 0036, the gateway-internal classification produces these gate behaviors:

| Gateway classification | HIGH approval gate | Audit / notes |
|---|---|---|
| `broker_verified` | passes per ADR 0035/0036 narrowing rules | trusted |
| `env_attested` | passes per ADR 0035/0036 narrowing rules; future ADR (0038 candidate) MAY tighten `ai-agent` to broker-only | self-attested, accepted |
| `unknown` | **fail-closed for HIGH severity throughout** — both during the §Migration window alignment and after 2026-06-01 | unverified — see §Migration window alignment for the rationale of fail-closed-from-day-one for HIGH paths |
| `spoofed_broker` | **immediately fail-closed**, audit log `actor_type_source_spoof_attempt` | spoof attempt |

For dispatch / canary deploy paths governed by ADR 0035 §Layer 2 (`ApproveAction`), the gateway continues to use the existing `ValidateApproverPermitted` semantics; classification is recorded for audit but does not block dispatch flows.

### Carry-point extension to ADR 0036's `approvalActionValue` (this ADR amends)

ADR 0036 §Carry point 2 added `RequesterActorType` to `approvalActionValue` so that Slack click-time policy can read it. v3's HIGH approval gate at click time also requires the gateway-internal classification AND `initiating_actor_type` to be available, but ADR 0036 does not carry them. This ADR amends `approvalActionValue` with two additional fields:

- `RequesterActorSource string` (json: `requester_actor_source`) — carries the **classification value** the gateway derived at the time it built the button payload (one of `broker_verified` / `env_attested` / `unknown` / `spoofed_broker`). Note: this is the gateway-internal classification, not the producer-input enum.
- `InitiatingActorType string` (json: `initiating_actor_type`, omitempty) — carries Axis 3's distal actor when the proximate `RequesterActorType` is `workspace-daemon`.

The gateway's `buildButtonValues` (per ADR 0036 §Carry point 2) SHALL populate both fields from the source DMail metadata at button-build time. The Slack `handleApprovalAction` (per ADR 0036 §Carry point 3) reads them and applies the §Gateway policy on classification table at click time. Tests in §Tests proving coverage cover both carries.

This addendum applies only to ADR 0036's `approvalActionValue` and does not require a separate amendment ADR; ADR 0036 §Carry point 2's button payload is explicitly extensible and the addition is additive (existing buttons without these fields decode cleanly via Go's `json.Unmarshal` ignore-unknown semantics, and the gateway interprets missing values as `unknown` / empty per the rules above).

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

- broker token claim (`caller_type`) — only used by the gateway-internal emit path; producers do not have access
- `RUNOPS_ACTOR_TYPE` env var — the **only** producer-side source of `requester_actor_type`
- `actor_type_source` audit attribute — closed enum `{ broker, env, unknown }`; producer-side emit path can only set `env` or `unknown` (= empty / no value)
- `initiating_actor_type` (REQUIRED for HIGH severity when `requester_actor_type=workspace-daemon`, optional otherwise) — emitted as separate metadata key

### Bypass candidates (post-rewrite, codex closure)

| Bypass | Closure |
|---|---|
| AI runtime forgets to set `RUNOPS_ACTOR_TYPE=ai-agent` | producer emits empty `requester_actor_type` → ADR 0036 empty-handling → fail-closed after 2026-06-01 (no tool_default silent mis-classification possible) |
| Daemon-laundered AI action with `initiating_actor_type` missing | gateway fail-closed at ADR 0036 §Carry point 3 for HIGH severity when `requester_actor_type=workspace-daemon` AND `initiating_actor_type` empty |
| Producer spoofs `actor_type_source=broker` | gateway reclassifies as `self_attested_broker_claim`, fail-closed immediately, audit log `actor_type_source_spoof_attempt` |
| Tool reads stale env var in long-lived process | tools SHALL re-read env per emit (test coverage required per emit site) |
| Producer emits `ai-agent` from heuristic | forbidden; tools MUST never write `ai-agent` outside of env (Axis 1.2) |

### Tests proving coverage (one per enforcement point)

- per-tool: emit DMail with `RUNOPS_ACTOR_TYPE=ai-agent` set → metadata contains `requester_actor_type=ai-agent` + `actor_type_source=env`
- per-tool: emit without env var → metadata contains `requester_actor_type=""` + `actor_type_source` either omitted or `unknown` (NO tool_default emission)
- per-tool: emit with `RUNOPS_ACTOR_TYPE=invalid_value` → producer rejects emission (= refuses to silently coerce to canonical or default)
- gateway-internal emit: receive authenticated request with broker token → emits with gateway-attached `actor_type_source=broker`
- gateway: receive DMail with `actor_type_source=broker` from producer → reclassify as `self_attested_broker_claim`, fail-closed for HIGH approval, audit log
- gateway: receive HIGH severity DMail with `requester_actor_type=workspace-daemon` and `initiating_actor_type` empty → fail-closed
- gateway: receive metadata with `actor_type_source=env` and `requester_actor_type=ai-agent`, second AI approver clicks → ADR 0035/0036 invariant fires
- gateway (effective_requester_actor_type rule): receive HIGH DMail with `requester_actor_type=workspace-daemon`, `initiating_actor_type=ai-agent`, AI approver clicks → `ErrAIAgentCannotApproveAIAgent` (= effective requester resolves to `ai-agent` via the daemon hop, AI-vs-AI invariant fires)
- gateway (effective_requester_actor_type narrowing): receive HIGH DMail with `requester_actor_type=workspace-daemon`, `initiating_actor_type=human-operator`, AI approver clicks → ack publishes (= effective requester resolves to `human-operator`, AI-vs-AI does NOT fire)

## Migration window alignment

ADR 0036 §Migration declared 2026-05-08 → 2026-05-31 as the window where empty `requester_actor_type` falls back to `human-operator`. ADR 0037 (this ADR) **narrows that allowance**: empty / `unknown` is fail-open only on **non-HIGH** paths (= dispatch / canary deploy where ADR 0035 §Layer 2 governs via `ApproveAction`). For HIGH severity convergence approval paths governed by ADR 0036 §Carry point 3, empty / `unknown` is fail-closed **from the day this ADR is Accepted** (i.e., before 2026-06-01).

The reasoning: ADR 0036's migration window was scoped to producer rollout time for low-severity flows. HIGH severity 4-eyes approval is the exact path where the AI-vs-AI invariant (ADR 0035) matters most; fail-open during the migration window for HIGH would mean "for 24 days, the invariant is best-effort". v3 of this ADR closes that gap.

| Severity / path | empty / `unknown` during migration window | post-2026-06-01 |
|---|---|---|
| HIGH severity 4-eyes (ADR 0036 path) | **fail-closed** (this ADR overrides ADR 0036's §Migration human-fallback for HIGH) | fail-closed |
| dispatch / canary deploy (ADR 0035 §Layer 2 path via `ApproveAction`) | human-fallback per ADR 0036 §Migration (unchanged) | fail-closed (per ADR 0036's existing migration plan, 0038 candidate may further verify) |

This ADR does NOT amend ADR 0036 §Migration directly; ADR 0036 stays correct for non-HIGH paths and remains the canonical reference for the dispatch path's migration end-state. v3 layers HIGH-path stricter handling on top — the union of ADR 0036 §Migration and this ADR §Migration window alignment is the actual operational contract.

A future ADR (0038 candidate) SHALL define acceptance criteria for the 2026-06-01 dispatch-path flip (= when does ADR 0036 §Migration's "fall back to human" terminate for non-HIGH). v3 deliberately does not pin this; it covers only HIGH-path tightening.

## Consequences

### Positive

- The four axes are decided once, not re-invented per tool, eliminating cross-tool drift in classification semantics.
- `actor_type_source` is **closed enum** `{ broker, env, unknown }` and gateway-attributed for `broker`, eliminating the v1 spoofing path codex (gpt-5.5) flagged.
- `tool_default` is forbidden, so a forgotten env var produces empty `requester_actor_type` that ADR 0036's empty-handling can fail-close on. The "AI request silently classified as human via tool_default" path is structurally impossible.
- `initiating_actor_type` is REQUIRED for HIGH severity workspace-daemon emissions, closing the daemon-laundering bypass.
- AI-agent classification is opt-in via explicit env (Axis 2). Existing human CLIs that do not set the env produce empty (= `unknown`) emissions. For HIGH severity paths these are fail-closed from ADR 0037 Accepted day; for non-HIGH paths they remain ADR 0036 §Migration human-fallback through 2026-06-01.

### Negative

- Producer rollout is per-tool. Until each tool implements ADR 0037, HIGH severity emissions from that tool fail-closed (= operator visible immediately) and non-HIGH emissions stay ADR 0036 §Migration human-fallback. Producer rollout is on the critical path for HIGH-severity coverage from the day this ADR is Accepted.
- AI agent runtime contract (Axis 2: "SHALL set RUNOPS_ACTOR_TYPE=ai-agent") is operator-discipline. A forgotten env var produces empty `requester_actor_type` and fail-closes HIGH approvals from Accepted day, surfacing the discipline gap loudly rather than silently mis-classifying.
- `actor_type_source` field broadens the audit log surface; ops dashboards will need to be updated to surface the new dimension and the new spoof-attempt audit event.

### Neutral

- ADR 0036 is NOT superseded. ADR 0037 provides the producer-side counterpart of ADR 0036's gateway-side enforcement. The two are paired.
- Per-tool ADRs (see §Per-tool ADR placeholder) declare emit site path + broker participation + `initiating_actor_type` participation. They do NOT declare per-tool defaults (this ADR forbids the concept).
- A future ADR (0038 candidate) will revisit (a) broker-only `ai-agent` classification (= even `actor_type_source=env` with `ai-agent` may require broker provenance) and (b) ADR 0036 fail-closed flip verification criteria for the 2026-06-01 trigger. The 0038 set is genuinely deferrable because the v2 of this ADR has eliminated the silent-bypass paths that made v1 "0038 dependent".

## Per-tool ADR placeholder

Each producing tool SHALL ship a per-tool ADR after this one is Accepted, declaring:

- the tool's emit site path (= row in §Enforcement inventory)
- whether the tool participates in the broker token path (Axis 1.1)
- whether the tool emits `initiating_actor_type` (Axis 3) — REQUIRED for any tool that may emit HIGH severity convergence DMails as `workspace-daemon`
- the tool's behavior when `RUNOPS_ACTOR_TYPE` is set to a non-canonical value (= reject the emission per Axis 1)

**No per-tool defaults are permitted.** Per Axis 1, if neither broker context nor a canonical env value is available, the producer emits empty `requester_actor_type` and the gateway handles via ADR 0036 §Carry point 3.

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
| 0036 amendment for `approvalActionValue` extension | gateway-side: add `RequesterActorSource` + `InitiatingActorType` to `approvalActionValue`, populate in `buildButtonValues`, read in `handleApprovalAction` per §Gateway policy on classification | 1 |
| 0037 per-tool ADR | each tool drafts its own ADR pinning emit site + broker participation + `initiating_actor_type` participation + `RUNOPS_ACTOR_TYPE` non-canonical handling. **No per-tool defaults** (this ADR forbids). | 4 |
| 0037 per-tool implementation | each tool implements emit + tests per §Enforcement inventory | 4 (one per tool, possibly split phase) |
| 0038 candidate (dispatch-path fail-closed flip + broker-only `ai-agent`) | define ADR 0036 §Migration end-state for non-HIGH paths + broker-only `ai-agent` classification | 1 (future, contingent on rollout coverage) |

## Refs

- ADR 0035 (architectural pin: AI vs AI invariant)
- ADR 0036 (Phase 4a path extension + 4 carry points + migration window)
- ADR 0019 (4-eyes approval, narrowed by 0035)
- ADR 0032 (broker grant matrix, 4 caller-type taxonomy)
- ADR 0027 (carry point discipline reference)
- codex (gpt-5.5) session retrospective 2026-05-08 (root cause: enforcement point inventory absence; framework: §Enforcement inventory section)
- refs#0011 (cross-repo issue this ADR completes the closing bracket of)
