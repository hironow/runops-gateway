# 0028. dmail-receiver multi-project routing

**Date:** 2026-05-07
**Status:** Accepted

## Context

ADR 0027 fixed how `project_id` propagates through the gateway dispatch
path (parser → button payload → Pub/Sub attribute). The downstream
consumer of that attribute is the workspace-VM `dmail-receiver` daemon
(ADR 0023): it pulls D-Mail messages from the dmail-inbound subscription
and writes them as `.md` files into a phonewave-watched outbox.

Until issue #0006, the receiver was single-mode: one
`PHONEWAVE_OUTBOX_DIR` env, one `OutboxWriter`, no project awareness.
That worked for the pre-multiplex world where each workspace VM hosted
exactly one project. For 1 VM = N project (the choice in
multiplex-discussion.md), the receiver has to look at the message
attribute and route the file to the right project's outbox — otherwise
the multiplex routing decisions made upstream (Slack `--project`,
Firestore registry) are lost the moment the message hits disk.

A naive "always read project_id" approach would break every existing
single-mode deployment the moment it sees a multiplex publisher attach
the new attribute. ADR 0027 lets project_id propagate by default; the
receiver must handle that without crashing the upgrade path.

## Decision

The receiver supports two modes selected at startup by env vars:

| Mode         | Trigger env                                  | Routing behaviour                          |
|--------------|----------------------------------------------|--------------------------------------------|
| **single**   | `PHONEWAVE_OUTBOX_DIR` only                  | one writer; project_id attribute ignored   |
| **multi**    | `PHONEWAVE_OUTBOX_DIRS_BY_PROJECT` (set)     | project_id required, fail-closed on misses |

Both env vars set is allowed during transition — the multi-mode map
takes precedence and the legacy single env is logged as deprecated.

### OutboxRouter — interface in `internal/adapter/input/pubsub`

```go
type OutboxRouter interface {
    Resolve(ctx context.Context, m Message) (Writer, error)
}
```

Two implementations:

- `SingleOutboxRouter` — wraps one `Writer`; `Resolve` does not call
  `m.Attributes()`. The pre-#0006 deployment behaviour is preserved
  byte-for-byte even when upstream publishers (gateway #0008) attach
  `project_id`.
- `MultiOutboxRouter` — wraps `map[string]Writer`; `Resolve` reads
  `m.Attributes()["project_id"]`, validates it with
  `domain.ValidateProjectID`, and looks it up in the map. Empty,
  malformed, or unmapped values return `ErrProjectNotRouted` so the
  caller nacks the message; Pub/Sub `max_delivery_attempts=5` then
  ships it to the DLQ for operator triage (existing
  `tofu/subscriptions.tf`).

`Receiver` itself is mode-agnostic: it depends on the router interface
and surfaces `ErrProjectNotRouted` via nack. The mode lives in the
composition root (`cmd/dmail-receiver/main.go`).

### Why interface lives in input/pubsub, not output/phonewave

Earlier plan iterations placed the router interface in
`internal/adapter/output/phonewave` so it could co-locate with
`OutboxWriter`. That requires the output adapter to import the input
adapter's `Message` type — a hexagonal-architecture violation that
also risks an import cycle. By keeping the router in input/pubsub the
dependency direction stays input → output → core/domain, and phonewave
remains a leaf with one responsibility (atomic file writes).

### Env parser fail-loud at boot

`phonewave.ParseOutboxDirsByProject` decodes
`id1:/abs/path/1,id2:/abs/path/2` and rejects:

- empty entries / leading or trailing comma
- entries without `:` separator
- ids that fail `domain.ValidateProjectID` (regex / length)
- relative paths (`filepath.IsAbs` required)
- duplicate ids (typo would silently overwrite)

A failed parse aborts process startup before any subscriber is created.
Operators discover misconfiguration in the deploy log, not in
mysterious DLQ traffic days later.

## Consequences

### Positive

- Single source of truth for project_id format
  (`domain.ValidateProjectID`) is shared across the parser, the
  publisher (gateway #0008), and the receiver.
- Single-mode deployments are byte-identical to pre-#0006: a simple
  grep against `SingleOutboxRouter` proves no project_id read.
- Multi-mode is fail-closed: an unknown project_id can never silently
  drop into the wrong outbox; it goes to the DLQ where the operator
  decides.
- Composition root chooses the mode at boot, so the rest of the
  receiver (filename validation, atomic write, OTel spans) is
  unchanged.
- New CI `pubsub-integration` job exercises both modes against the
  emulator, so production cutover risk is bounded by what the test
  suite proves rather than by what an operator remembers to do
  manually.

### Negative

- Two modes to maintain instead of one. Mitigated by sharing
  `Receiver` and the `OutboxWriter` infrastructure; only the router
  implementation differs.
- Operators upgrading to multi-mode must add the new env var **and**
  republish any DLQ-stuck messages with the right project_id. The
  alternative (silent fallback to a default outbox) was rejected
  because it makes routing failures invisible.
- `:` is the env-var key/value delimiter, which collides with Windows
  drive letters (`C:\...`). The receiver runs on Linux workspace VMs
  (ADR 0023), so the collision is not realized in production. CLAUDE.md
  flags Windows compatibility as SHOULD; if a Windows port surfaces
  later, the parser will need a richer separator.

### Neutral

- Pub/Sub message attribute size grows by one entry (≤ 64 bytes) when
  multiplex publishers add project_id. Far below the 1 MB Pub/Sub
  attribute limit and the same as ADR 0027's outbound carry.
- The OTel span attribute `project_id` is now recorded for non-empty
  values, joining the existing `kind`, `target_tool`, and
  `idempotency_key` spans. DLQ runbook (`docs/runbooks/dlq.md`)
  already advises operators to inspect attributes; project_id slots
  into that workflow.

## Out of scope (deferred)

- **dmail-emitter writes project_id into D-Mail YAML frontmatter** —
  issue #0007. The receiver only reads the Pub/Sub attribute; mirroring
  it into the body so workspace tools can read it without parsing
  attributes is the emitter's responsibility.
- **Workspace VM env distribution** (`PHONEWAVE_OUTBOX_DIRS_BY_PROJECT`
  per VM via systemd) — exe-coder repo issue #0010.
- **Operator HTTP admin endpoint for the registry** — issue #0012.
- **Default project resolution when project_id is empty in multi-mode** —
  intentionally rejected. An operator who forgot `--project=foo` should
  see a DLQ failure, not a guess that lands in some "default" project.
  Future #0013 (lifecycle CLI) may surface a per-operator preference,
  but that is a Slack-side decision, not a receiver-side one.

## Why fail-closed in multi-mode

A multi-mode receiver that silently fell back to a default outbox when
project_id is missing or unmapped would route messages **based on a
guess**. The operator's intent (Slack `--project=foo`) would be silently
overridden. By failing the message into the DLQ instead, the operator
sees the routing failure surface where it can be triaged
(`docs/runbooks/dlq.md`); the producer side (Slack handler) can be
fixed; and the message can be republished correctly.

## Cutover (single-mode → multi-mode, per workspace VM)

1. Operator adds `PHONEWAVE_OUTBOX_DIRS_BY_PROJECT=foo:/path/foo,bar:/path/bar`
   to the workspace VM systemd unit env (exe-coder #0010).
2. Operator restarts dmail-receiver via systemd.
3. The legacy `PHONEWAVE_OUTBOX_DIR` env can stay during transition;
   the boot log will warn it is deprecated.
4. Slack publishers (gateway #0008) start sending project_id; multi-mode
   routing kicks in.
5. Any message that arrives without a recognized project_id (e.g. from
   an old publisher path or a typoed `--project`) lands in the DLQ.
6. Operator triages the DLQ per the runbook and either republishes with
   the right project_id or fixes the publisher.
7. Once the deployment is stable, operator removes
   `PHONEWAVE_OUTBOX_DIR` from the systemd unit.

## References

- ADR 0013 — Pub/Sub bridge for outbox (publish path)
- ADR 0023 — dmail-daemon OCI image deployment (receiver runs on
  workspace VM)
- ADR 0024 — IaC test split (variable validation guardrail used by
  `tofu/subscriptions.tf` DLQ config)
- ADR 0025 — port/adapter dual strategy (registry SoT)
- ADR 0026 — Firestore production deploy (registry storage)
- ADR 0027 — project_id metadata carry standard (publish path)
- Issue #0006 (this PR), #0007 (dmail-emitter, future), #0010
  (workspace VM env, exe-coder)
- `internal/adapter/input/pubsub/router.go` — OutboxRouter interface
- `internal/adapter/input/pubsub/receiver.go` — mode-agnostic receiver
- `internal/adapter/output/phonewave/writer.go` —
  `ParseOutboxDirsByProject`
- `tofu/subscriptions.tf` — DLQ subscription wiring
  (`max_delivery_attempts=5`)
- `docs/runbooks/dlq.md` — DLQ triage workflow
