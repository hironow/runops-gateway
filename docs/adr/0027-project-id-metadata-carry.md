# 0027. `project_id` metadata carry standard (Slack → Pub/Sub dispatch path)

**Date:** 2026-05-07
**Status:** Accepted

## Context

Issue #0009 introduced the multiplex project registry, ADR 0025 chose the
port/adapter dual strategy, and ADR 0026 fixed the production storage
backing. Once a project_id exists in the registry, every dispatch
originated by an operator must reach the target tool with the same
project_id intact — otherwise the multiplex routing is purely
declarative and the receiver has no way to know which workspace a
request belongs to.

Issue #0008 (Slack `/agent --project=<id>` flag) is the first place
where project_id enters the pipeline. The dispatch path goes:

1. operator types `/agent paintress --project=foo "fix it"` in Slack
2. `CommandHandler` parses + validates against the registry
3. `BuildDispatchConfirmation` renders an ephemeral Block Kit with an
   approve button
4. operator clicks Approve
5. `InteractiveHandler.handleDispatchAction` reconstructs a
   `DispatchRequest`
6. `PubsubDispatcher.Dispatch` publishes to the dmail-inbound topic
7. dmail-receiver / 5 tools consume the message

There are three places between (1) and (7) where project_id can fall
out: the Slack form parser, the gzip+base64 button payload, and the
Pub/Sub message attributes. Without a standard each gateway commit
might handle one and forget the others.

This ADR fixes that standard for the dispatch path so future code keeps
the contract; approval and dmail-emitter paths are deferred to their
own issues.

## Decision

`project_id` propagates through three carry points in the gateway, each
identical in semantics:

| # | Carry medium                                                                | Layer                                |
|---|------------------------------------------------------------------------------|--------------------------------------|
| 1 | parsed flag (`--project=<id>` / `--project <id>`) on the Slack form input    | `slack.parseSlashCommandText`        |
| 2 | gzip+base64-compressed JSON button payload (ADR 0011) — `dispatchActionValue.ProjectID` | `slack.buildDispatchButtonValues` / `parseDispatchActionValue` |
| 3 | Pub/Sub message attribute `project_id` (alongside existing attributes)       | `dispatcher.PubsubDispatcher.Dispatch` |

Empty project_id is a first-class value: when the operator does not
pass `--project`, project_id is "" and:

- the parser returns `("", "")` for project + empty rendering branch in Block Kit
- the button payload omits the field via `json:",omitempty"`
- Pub/Sub omits the attribute (presence-of-key is the routing signal)

`domain.ValidateProjectID` is shared between the parser and the
registry adapters so format rules (`^[a-zA-Z0-9_-]+$`, max 64) cannot
drift between layers.

### Validation gate

The Slack handler calls `port.ProjectRegistry.Get(ctx, projectID)` and
rejects in three cases:

- `ErrProjectNotFound` → ephemeral "project not registered: <id>"
- non-active status → ephemeral "project is archived: <id>"
- registry disabled (handler instantiated without `WithProjectRegistry`)
    - `--project` supplied → ephemeral "registry disabled" — fail-closed

Validation runs **before** the button payload is built, so an unknown
project_id never reaches Pub/Sub, never gets stored in a button value
that could later be replayed, and never reaches the 4-eyes approval
flow.

## Out of scope (deferred to future ADRs)

- **4-eyes approval flow carry**: `approvalActionValue` /
  `handleApprovalAction` / `ApprovalRequester` are intentionally
  untouched. Approval gates a HIGH-severity convergence DMail rather
  than the dispatch itself; whether project_id should constrain who
  may approve, and what happens when the original DispatchRequest
  carried a project but the approver's session does not, deserves its
  own ADR. Until that decision lands, the approval flow operates as
  before — agnostic to project_id.
- **D-Mail frontmatter `metadata.project_id`**: the gateway publishes
  project_id as a Pub/Sub attribute; mirroring it into the D-Mail YAML
  body is the responsibility of the dmail-emitter (issue #0007) so
  that workspace-side tools see it whether they read attributes or
  YAML.
- **dmail-receiver routing on project_id**: implemented in #0006
  (workspace VM env-driven outbox path).
- **Default project resolution**: when `--project` is unspecified
  the dispatch carries an empty value; the registry is not consulted
  for a "default" project. A heuristic (e.g. last project the user
  used) belongs in a future lifecycle CLI (#0013), not in the
  dispatch path.

## Consequences

### Positive

- One source of truth (the parser output) flows through three carry
  points unchanged, eliminating the class of bug where project_id is
  set in step 1 but lost by step 3.
- `domain.ValidateProjectID` is the only place format rules live;
  registry / parser / Pub/Sub all agree by construction.
- Fail-closed validation means an unknown project_id never reaches
  the bus, the 4-eyes flow, or any persistent store.
- Existing non-multiplex deployments are byte-identical: project_id
  defaults to "" everywhere and every layer omits the empty case.

### Negative

- `dispatchActionValue` gains one optional field. Older button
  payloads still in flight (e.g. an operator who clicked Approve
  before the deploy) decode with project_id="", which is the correct
  behaviour but means the Block Kit confirmation UI for those
  in-flight clicks will not show the Project line. Acceptable since
  in-flight Slack approvals are short-lived (operator typically
  decides within seconds).
- Operators must remember to pass `--project=<id>` for multiplex
  deployments. No enforcement of "project required" lands in this
  PR; a future ADR can add it once the dmail-emitter / receiver
  side picks up the same value.

### Neutral

- Pub/Sub message attribute size grows by one entry (≤ 64 bytes per
  message) when project_id is set. Far below the 1 MB Pub/Sub
  attribute limit.
- gzip+base64 button value grows by a handful of bytes; well below
  Slack's 2000-character button value limit.

## Why dispatch path only

Approval gating, D-Mail emission, and dmail-receiver routing each have
their own multiplex contracts that depend on context this ADR does not
have (4-eyes invariants, schema versioning, workspace VM topology).
Mixing those into one ADR would have coupled scope decisions across
four issues. Keeping the dispatch path standalone lets each downstream
issue write its own ADR with the right context.

## Why fail-closed validation

A registry-enabled deployment that silently dispatches `--project=foo`
without checking the registry is worse than no validation: the
operator's intent (route to project foo) is on record, but the
infrastructure honours that intent based on a value the gateway never
verified. If foo is a typo, or has been archived, the dispatch reaches
the wrong workspace. Failing the click before it produces a button
payload (and thus before any Approve can run) is the cheapest
intervention point.

## References

- ADR 0011 — gzip+base64 button value compression
- ADR 0014 — Slack notification centralized in runops-gateway
- ADR 0019 — HIGH severity 4-eyes approval (the path explicitly out
  of scope here)
- ADR 0025 — port/adapter dual strategy
- ADR 0026 — Firestore production deploy
- Issue #0008 (this PR), #0009 (registry SoT), #0011 (Firestore adapter),
  #0007 (dmail-emitter project_id, future), #0006 (dmail-receiver
  routing, future)
- `internal/adapter/input/slack/command.go` — parser + validation
- `internal/adapter/input/slack/dispatch_action.go` — button payload
- `internal/adapter/output/dispatcher/pubsub.go` — Pub/Sub attribute
