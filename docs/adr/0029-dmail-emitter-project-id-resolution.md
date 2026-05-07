# 0029. dmail-emitter project_id resolution

**Date:** 2026-05-07
**Status:** Accepted

## Context

ADR 0027 fixed how `project_id` rides the dispatch path (Slack →
Pub/Sub attribute), and ADR 0028 covered how the receiver consumes it
on the workspace VM. Issue #0007 closes the loop on the **emit** path:
when one of the five tools writes a D-Mail to its archive directory,
the `dmail-emitter` daemon picks it up via fsnotify and republishes it
on `dmail-outbound`. Without project awareness, the resulting message
loses the multiplex routing identity that ADRs 0027/0028 worked to
preserve.

The emitter sees three sources of truth for project_id:

1. The **archive path** the file appeared at — operator-controlled via
   the systemd unit env (`PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT`).
2. The **D-Mail YAML frontmatter** `metadata.project_id` — written by
   whichever pillar emitted the file.
3. **Nothing** — pre-#0007 deployments simply do not set project_id at
   all and `dmail-outbound` consumers ignore the attribute.

Without an ADR these three sources can disagree silently and route
messages to the wrong project. With multi-mode the operator's intent
is the env mapping; the frontmatter can lag (older tooling, stale
templates) or be wrong outright.

## Decision

The emitter delegates project_id resolution to an `ArchiveRouter`
interface in `internal/adapter/input/phonewave`. Two implementations
mirror the `OutboxRouter` pair from ADR 0028, but in the reverse
direction (path → id):

| Router                | Trigger env                                  | Behaviour                                 |
|-----------------------|----------------------------------------------|-------------------------------------------|
| `SingleArchiveRouter` | `PHONEWAVE_ARCHIVE_DIRS` only                | always returns `("", nil)` — frontmatter pass-through |
| `MultiArchiveRouter`  | `PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT` set      | resolves by archive-dir prefix; unmapped paths return `ErrPathNotMapped` |

Both env vars set is allowed during transition — multi-mode wins and
the legacy single env is logged as deprecated.

### Resolution rules (multi-mode)

When `MultiArchiveRouter.ResolveProjectID` succeeds:

1. If `frontmatter.project_id` is empty or matches the path-derived id,
   the path-derived id is written into `mail.Metadata["project_id"]`.
2. If `frontmatter.project_id` differs from the path-derived id,
   **path-derived wins**. A warn-log records both values so the
   operator can spot stale tooling. The rationale is operator intent:
   the systemd env is what the operator knowingly pinned at deploy
   time, while frontmatter is whatever the tool happened to write.

The publisher (`internal/adapter/output/pubsub/publisher.go`) was
already merging `mail.Metadata` into Pub/Sub message attributes; ADR
0027's contract therefore continues to hold without any publisher
change.

### Skip on unmapped path

When `ResolveProjectID` returns `ErrPathNotMapped` (multi-mode and the
archive dir is not in the map), the emitter:

- clears its dedup record so a future fsnotify event for the same path
  can retry
- logs a warn (path + error)
- emits a span event `skip{reason=path_not_mapped}`
- returns nil (no nack to the underlying transport — this is a watcher,
  not a subscriber)

The file stays on disk for operator triage. Skipping rather than
failing keeps the emitter's read-only contract intact.

### Nested archive dirs are forbidden

`NewMultiArchiveRouter` rejects any pair of archive dirs that overlap:
identical cleaned paths and any prefix relationship (in either
direction) abort process startup. Nesting was tempting because it
would let an operator carve a subset of one project's tree into
another, but in practice it produces ambiguous routing — `/work/a/sub/x`
could match both `/work/a` and `/work/a/sub`. Forbidding the
configuration entirely makes `ResolveProjectID` order-independent and
easier to reason about; deployments that genuinely need overlap can
introduce dedicated subdirectories per project.

### Cross-platform list separator

Earlier versions of the loader used `strings.Split(env, ":")` for the
legacy `PHONEWAVE_ARCHIVE_DIRS` list. That shreds Windows drive
letters (`C:\foo`). The loader now uses `filepath.SplitList` so the
list separator follows the OS (`:` on Linux/macOS, `;` on Windows).

### Peer-mode handshake (`PHONEWAVE_PEER_RECEIVER_MODE`)

Emitter and receiver are independent binaries on different hosts.
Without coordination, an operator can boot a single-mode emitter
against a multi-mode receiver — every message becomes a DLQ traveler.
A new optional env, `PHONEWAVE_PEER_RECEIVER_MODE`, lets the operator
declare the peer's mode at the emitter:

- when set, the emitter exits with a clear error if its own
  `router.Mode()` differs from the declared peer mode;
- when unset, the emitter logs a warn and proceeds (legacy
  compatibility).

The receiver does not need a symmetric env: the receiver-side router
already nacks unmapped messages, which surfaces mismatches via DLQ
runbooks rather than silently. Future enhancements (e.g. an actual
mode handshake message on the bus) can build on top of this; they are
out of scope for #0007.

## Consequences

### Positive

- One router interface owns the path → id contract; the emitter is
  mode-agnostic.
- Path-derived value wins on conflict, so operator intent (env) cannot
  be silently overridden by stale frontmatter.
- Unmapped paths skip, not fail — files stay on disk for triage and the
  emitter's read-only invariant is preserved.
- Nested-dir reject and cross-platform list separator close two
  concrete production failure modes (ambiguous routing, Windows path
  shred).
- Peer-mode handshake gives operators an opt-in fail-fast for a
  cross-binary configuration class that would otherwise live entirely
  in deployment culture.

### Negative

- One more env var (`PHONEWAVE_PEER_RECEIVER_MODE`). Default-unset
  behaviour is the same as before, so legacy deploys keep working;
  modern deploys add one line to the systemd unit.
- The `phonewave.ParseOutboxDirsByProject` rename leaves a
  one-PR-cycle deprecated alias; remove it in the follow-up to keep
  the package surface minimal.

### Neutral

- `domain.ValidateProjectID` (the regex shared with #0008/#0009) is
  reused at parser time, so format drift between Slack-side publishers
  and emitter-side consumers cannot happen.
- The publisher is unchanged; everything we add reaches Pub/Sub via
  the existing `mail.Metadata` carry path.

## Out of scope (deferred)

- **Frontmatter writer side** — the five pillars are responsible for
  setting `metadata.project_id` correctly when they emit a D-Mail.
  The emitter cannot fix bad inputs; it can only surface them via
  warn-logs (mismatch) or skip (unmapped).
- **Workspace VM env distribution** —
  `PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT` and the new
  `PHONEWAVE_PEER_RECEIVER_MODE` arrive on each VM via the exe-coder
  systemd template (issue exe #0010).
- **Operator HTTP admin endpoint** — issue #0012.
- **Default project resolution when path is unmapped** — intentionally
  rejected. An operator who forgot to register `/work/foo` should see
  a skip, not a guess.

## Cutover (legacy → multi-mode, per workspace VM)

1. Operator extends the systemd unit env with
   `PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT=foo:/abs/foo,bar:/abs/bar` and
   `PHONEWAVE_PEER_RECEIVER_MODE=multi`.
2. Operator restarts dmail-emitter via systemd. Boot logs print the
   mode and the project count.
3. Files dropped into `/abs/foo` publish with `project_id=foo` on
   `dmail-outbound`; files in unregistered dirs skip with a warn.
4. The legacy `PHONEWAVE_ARCHIVE_DIRS` env can stay during transition;
   the boot log will warn it is deprecated.
5. Once the deployment is stable, operator removes
   `PHONEWAVE_ARCHIVE_DIRS` from the systemd unit.

## References

- ADR 0013 — Pub/Sub bridge for outbox (publish path)
- ADR 0023 — dmail-daemon OCI image deployment (emitter runs on
  workspace VM)
- ADR 0027 — project_id metadata carry standard (Slack → Pub/Sub
  attribute)
- ADR 0028 — dmail-receiver multi-project routing (receive path)
- Issue #0007 (this PR), #0006 (receiver, merged), #0008 (Slack flag,
  merged)
- `internal/adapter/input/phonewave/router.go` — ArchiveRouter
- `internal/adapter/input/phonewave/emitter.go` — resolution + warn
- `internal/adapter/output/phonewave/writer.go::ParseDirsByProject` —
  shared env parser
- `cmd/dmail-emitter/main.go` — env wiring + peer-mode guard
