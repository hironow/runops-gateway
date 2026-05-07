# 0026. Firestore production adapter for the project registry

**Date:** 2026-05-07
**Status:** Accepted

## Context

ADR 0025 committed the gateway to a port/adapter dual strategy for the
multiplex project registry:

- **SQLite** (#0009, merged): dev / test / operator local Mac
- **Firestore native** (#0011, this PR): production / staging Cloud Run

The first implementation PR (#0011) had to make four concrete decisions
that could not be deferred to "future work" without a real production
risk, all surfaced and pushed by codex review v1–v3:

1. How does Firestore avoid colliding with whatever already lives in the
   GCP project's `(default)` database?
2. How does the Firestore client's lifecycle fit into the existing
   composition root so we do not leak gRPC connections from Cloud Run?
3. How do we prove the adapter actually works against an emulator, not
   just compiles, before flipping production traffic?
4. How does the same code path serve both the emulator (CI / local) and
   the production deployment without code-level forks?

## Decision

### Named Firestore database (`runops-registry`), not `(default)`

We create `google_firestore_database.runops_registry` with `name =
var.firestore_database_name` (default: `"runops-registry"`). A GCP
project may have any number of named databases; only one `(default)` DB
per project is allowed. By naming our DB we guarantee that
`tofu apply` succeeds regardless of whether the project was previously
touched by Firebase, App Engine, Datastore mode, or another team.

`delete_protection_state = "DELETE_PROTECTION_ENABLED"` blocks
`tofu destroy` from removing the SoT for project_id by mistake;
operators must run `gcloud firestore databases update --delete-protection=DISABLED`
explicitly first.

### Factory cleanup contract — `(reg, cleanup, err)` triple

`state.NewProjectRegistryFromEnv` was extended from `(port.ProjectRegistry, error)`
to `(port.ProjectRegistry, CleanupFunc, error)`. The cleanup is non-nil
on every code path (a `noopCleanup` is returned on errors so callers
can defer unconditionally).

For Firestore the cleanup is `client.Close`; for SQLite it is
`db.Close`. The composition root in `cmd/runops/main.go` defers it from
`main()` so a Cloud Run instance shutting down releases the gRPC
streams cleanly. Without this, long-running Cloud Run instances would
leak file descriptors and Firestore connections over time.

### Emulator routes through `(default)` DB; production uses the named DB

The factory branches on `RUNOPS_FIRESTORE_DATABASE`:

- empty (`""`) → `firestore.NewClient(ctx, projectID)` — uses the
  `(default)` database. This is the path the emulator and CI use, and
  it works against every release of the Firestore emulator we have
  tested.
- non-empty → `firestore.NewClientWithDatabase(ctx, projectID, name)` —
  uses the named DB. Production Cloud Run sets
  `RUNOPS_FIRESTORE_DATABASE=runops-registry` via tofu so the client
  routes to the registry DB.

The same Go code services both environments; only the env var
differs. CI does not depend on emulator named-DB support, which has
shifted between firebase-tools releases.

### CI integration job is the cutover gate

`.github/workflows/ci.yaml` now contains a `firestore-integration` job
that builds the firebase-emulator image (Pub/Sub + Firestore bundled),
starts the container, runs `scripts/init-firestore.sh` for a sentinel
round-trip, and then executes
`go test -tags=integration -run Firestore ./internal/adapter/output/state/...`.
A failing job blocks PR merge.

Without this gate, the only signal that the Firestore adapter actually
works would be production traffic — and discovering a wire-protocol
mismatch in production is the worst possible time.

## Consequences

### Positive

- Tofu apply is safe in any GCP project layout (named DB sidesteps
  `AlreadyExists` on `(default)`).
- Cloud Run instance shutdowns release Firestore gRPC connections
  deterministically; no slow-burn leak.
- CI proves the adapter round-trips through a real Firestore emulator
  before merge — production cutover risk is bounded.
- Same code path on dev / CI / production; `RUNOPS_FIRESTORE_DATABASE`
  is the only env var that differs.

### Negative

- One additional Cloud Run env var (`RUNOPS_FIRESTORE_DATABASE`) that
  operators must remember to set. Mitigated by tofu setting it via the
  Cloud Run service definition rather than relying on operator shell
  state.
- The cutover from operator-local SQLite to Firestore is a manual
  process for now — operators run `runops project list`, then re-add
  each project against the production Firestore adapter. Documented
  below; an automated seed tool is deferred to a follow-up issue
  (`#0013` lifecycle CLI is the most likely home).
- Project-level `roles/datastore.user` grants the SA access to every
  Firestore database in the project, not just `runops-registry`. We
  accept this for Phase α; per-database IAM is in preview and we will
  revisit when GA.

### Neutral

- Adding a second adapter doubles the surface area for registry tests,
  but the `port.ProjectRegistry` contract is shared — the test
  scenarios (8 cases per adapter) mirror each other and document the
  contract by example.
- The firebase-emulator image base changed from `node:22-slim` to
  `eclipse-temurin:21-jre-noble` because firebase-tools >= 14 dropped
  JDK 17 support. Image size grew but Pub/Sub + Firestore now both
  work in a single container.

## Cutover procedure (operator local SQLite → production Firestore)

1. Operator confirms production gateway is **not** yet pointed at
   Firestore (`RUNOPS_PROJECT_REGISTRY=firestore` not set in tofu
   cloud_run env).
2. `tofu apply` against the production project — creates
   `runops-registry` named DB and grants `roles/datastore.user` to
   `chatops_sa`.
3. Operator on local Mac runs `runops project list --status all` and
   captures the row data (e.g. as JSON via a one-off script).
4. For each row, operator runs `runops project add ...` against a
   shell configured for Firestore:
   ```bash
   export RUNOPS_PROJECT_REGISTRY=firestore
   export GOOGLE_CLOUD_PROJECT=hironow-runops-prod
   export RUNOPS_FIRESTORE_DATABASE=runops-registry
   gcloud auth application-default login   # one-time
   runops project add ...
   ```
5. Operator updates the production gateway tofu module to set
   `RUNOPS_PROJECT_REGISTRY=firestore` and
   `RUNOPS_FIRESTORE_DATABASE=runops-registry` in cloud_run env, then
   `tofu apply` to restart the service.
6. Operator verifies via `runops project list` (with the same env
   above) that the registry round-trips through Firestore.

A shell script automating steps 3-4 is intentionally not part of this
PR — Phase α has at most a handful of projects, and the cutover ADR
serves better than premature automation. Issue #0013 (project lifecycle
CLI) will pick this up when there is a real operational need.

## Alternatives considered

1. **Use `(default)` DB everywhere**: rejected. Apply would fail on
   any project with pre-existing Firestore use. We would forever be
   one customer-environment shape away from a deploy halt.
2. **Leave the named DB to operators (no tofu)**: rejected. The
   registry is the SoT; managing it outside IaC means drift between
   what's deployed and what we can reproduce.
3. **Per-database IAM** (preview): rejected for Phase α. Project-level
   `roles/datastore.user` is the documented stable path; we will
   revisit when per-DB IAM goes GA.
4. **Skip CI emulator job, rely on staging deploy**: rejected (codex
   v1 #1). Production cutover blocker means we need pre-merge proof.

## Why a single firebase-emulator container

We bundle Pub/Sub and Firestore in the same container instead of
spinning up two separate emulator services because:

- firebase-tools already orchestrates both (`firebase setup:emulators:*`).
- The container's healthcheck (`/emulators` on port 4400) covers both
  services in one probe.
- CI needs only one image build and one wait-for-healthy step.

The cost is a slightly larger image; the benefit is a markedly simpler
local + CI workflow.

## References

- ADR 0025 (port/adapter dual strategy — establishes the framework
  this ADR fills in)
- Issue #0011 (this PR)
- `internal/adapter/output/state/firestore_project_registry.go`
- `internal/adapter/output/state/registry_factory.go`
- `tofu/firestore.tf`, `tofu/iam_firestore.tf`,
  `tofu/tests/firestore_validation.tofutest.hcl`
- `.github/workflows/ci.yaml` — `firestore-integration` job
- `docker/firebase-emulator/Dockerfile`,
  `docker/firebase-emulator/firebase.json`
- `scripts/init-firestore.sh`
