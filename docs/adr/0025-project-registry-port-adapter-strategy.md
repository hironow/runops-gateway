# 0025. Project Registry — port/adapter dual strategy (SQLite dev, Firestore production)

**Date:** 2026-05-07
**Status:** Accepted

## Context

Issue #0009 introduces a `projects` registry as the SoT for the multiplex
`project_id` namespace (1 VM = N project, hironow decision 2026-05-06
option B). The registry must:

1. validate `project_id` format and reject duplicates
2. survive Cloud Run pod restarts in production (multi-instance safe)
3. be operable from the CLI in development without external infra
4. share its contract with the rest of the gateway (Slack flag, dispatch
   validation, GitHub App installation lookup)

A first review (codex) flagged two contradictions in the original SQLite-
only plan:

- **SoT collision**: a CLI default writing to `~/.runops/state.db` while
  the dispatcher reads a Cloud Run-side DB makes "1 VM = N project"
  ambiguous.
- **Cloud Run + SQLite persistence is unproven**: ephemeral disk loses
  state on redeploy, and GCS Fuse + SQLite is incompatible with file
  locking. SQLite cannot back the production registry.

A second review additionally surfaced:

- **Fail-open default = sqlite** would let env misconfiguration silently
  drop the production registry to a local file.
- **"gateway pod exec" CLI** is incompatible with Cloud Run's runtime
  (no `kubectl exec` equivalent).

## Decision

Adopt a **port/adapter dual strategy** for `port.ProjectRegistry`:

| Environment           | Adapter                          | DB shape     | Notes                                |
|-----------------------|----------------------------------|--------------|--------------------------------------|
| dev / test / local    | `state.SQLiteProjectRegistry`    | SQLite WAL   | this PR; modernc.org/sqlite          |
| production / staging  | `state.FirestoreProjectRegistry` | Firestore    | shipped in #0011; managed persistence |

Selection happens at the composition root via env vars, **fail-closed**:

- `RUNOPS_PROJECT_REGISTRY` is required (`sqlite` | `firestore`).
- `RUNOPS_ENV=development` is the **only** opt-in that defaults
  `RUNOPS_PROJECT_REGISTRY` to `sqlite`.
- An unset registry env in any other environment is an error
  (`RUNOPS_PROJECT_REGISTRY env required ...`).
- `firestore` returns `errors.New("firestore adapter not implemented yet,
  see issue #0011")` until #0011 lands.
- Any other value is rejected with `unknown RUNOPS_PROJECT_REGISTRY value`.

Production registry mutations flow through a **gateway HTTP admin
endpoint** (issue #0012, separate scope). The `runops project ...` CLI
remains operator-local, dev-only.

## Consequences

### Positive

- Multiplex Phase α blocker (#0009) lands in a single PR without dragging
  Cloud Run persistence ADR into the same change.
- Interface-driven design lets us swap adapters without touching dispatch
  / Slack / GitHub-App callers.
- tap 5-tool substrate canonical lock (S0037) stays on SQLite/WAL because
  tap is single-CLI; gateway uses the right tool for its Cloud Run role
  without forcing pattern uniformity.
- Fail-closed factory eliminates the silent-fallback class of incidents.

### Negative

- Two adapters to maintain instead of one. Mitigated by sharing the
  domain `Project` type, sentinel errors, and ID validation regex; only
  the storage substrate differs.
- Operators must remember to set `RUNOPS_PROJECT_REGISTRY=sqlite` (or
  `RUNOPS_ENV=development`) in local shells. Documented in the issue and
  surfaced in CLI error messages with remediation guidance.
- `firestore` value returns an error until #0011 ships; deploys that try
  to use it before then will refuse to start. This is intentional — see
  fail-closed rationale below.

### Neutral

- `_migrations` table tracks applied migration ids; SQLite-only, not
  shared with Firestore (Firestore doesn't need DDL migrations).
- The CLI subcommand uses `text/tabwriter` for `list` formatting; the
  Firestore adapter will reuse the same Project struct so the formatter
  remains shared.

## Alternatives considered

1. **SQLite + Cloud Run + persistent disk**: rejected — Cloud Run gen2
   persistent volume is GCS Fuse-backed; SQLite file locking semantics
   are incompatible.
2. **Cloud SQL PostgreSQL from day one**: rejected for Phase α — adds
   tofu + connection-management scope that delays the multiplex blocker
   without changing the interface contract.
3. **Firestore-only (no SQLite)**: rejected — every dev iteration would
   need the Firestore emulator running; CI cost goes up.
4. **GCE migration with SQLite**: rejected — abandons Cloud Run for the
   gateway, far larger blast radius than warranted.

## Why fail-closed

A registry that silently writes to local SQLite when env is missing is
worse than no registry: dispatch handlers would happily look up the
"wrong" SoT and route requests to operator-local rows that don't reflect
production reality. Failing fast with a clear error preserves the
invariant that **the production gateway can only run when
`RUNOPS_PROJECT_REGISTRY=firestore` is explicitly set**, even if #0011 is
not yet landed (in which case the gateway refuses to start, which is the
correct safe default).

## Why a CLI in this PR if it's dev-only

The CLI lets operators validate the registry contract on local Mac
without spinning up Firestore emulator, and it serves as a working
reference implementation for the future HTTP admin endpoint (#0012).
Without it, contributors would have to write integration tests against a
half-implemented registry.

## References

- `internal/core/port/port.go` — `ProjectRegistry` interface
- `internal/core/domain/project.go` — Project type + ID validation
- `internal/adapter/output/state/sqlite_project_registry.go` — SQLite impl
- `internal/adapter/output/state/registry_factory.go` — env-driven selection
- `tests/integration/project_lifecycle_test.go` — E2E lifecycle test
- Issue #0009 (this PR), #0010 (GitHub App installation_id validation),
  #0011 (Firestore adapter), #0012 (HTTP admin endpoint)
- D-Mail metadata v1.1 — `project_id` regex shared via this ADR's
  domain layer
