# 0043. Deploy-time OpenTofu drift gate

**Date:** 2026-06-02
**Status:** Accepted

## Context

`cd.yaml` runs `gate-check → check-changes → infra (tofu apply) → deploy` in
series. Three gaps let a deploy ship on top of infrastructure that no longer
matches OpenTofu state:

1. **`check-changes` only inspects the last commit** (`git diff --name-only
   HEAD~1 HEAD | grep '^tofu/'`). When a squash merge moves a `tofu/` change out
   of the `HEAD~1..HEAD` window, the `infra` job is skipped. This is not
   hypothetical: an 80-commit promote previously skipped `infra` on every run,
   leaving `tofu/` changes un-applied in production while code kept shipping.
2. **`deploy` runs even when `infra` is `skipped`** (`needs.infra.result ==
   'skipped'` is allowed). So a new image lands while drift sits un-reconciled.
3. **Out-of-band changes** (gcloud / console) drift live infra from state and
   are invisible to `check-changes` entirely.

The leretto-inc/meo-agent `terraform-drift-gate` composite action solves the
analogous problem by running `terraform plan -detailed-exitcode` just before
deploy and failing on drift. Its design assumes a *separate* `terraform-apply`
workflow running in parallel (hence its gh-api polling step). runops-gateway
applies infra as a *job in the same workflow*, so ordering is enforced by a job
`needs` dependency instead of polling.

Two repo-specific constraints shape the adaptation:

- The github provider (`github_repository_ruleset` ×3) is co-located in the same
  root module. Those rulesets are gated `count = var.github_repo_name == "" ? 0
  : 1`, and `cd.yaml` never sets `TF_VAR_github_repo_name`, so they are absent
  from CD-produced state. More importantly, the github provider needs no token at
  plan time **only while its `owner` is empty** — a non-empty `owner` makes
  provider configure call `api.github.com` and fail without a token.
- `tofu/main.tf` already declares `lifecycle { ignore_changes = [image] }` (Cloud
  Run) and `ignore_changes = [secret_data]` (secret versions), so image and
  secret-value churn never surface as drift. No chronic-drift allowlist is needed
  at introduction.

## Decision

Add a **deploy-time drift gate** that pins the invariant:

> **A deploy MUST NOT run on drifted `google_*` infrastructure.**

- A composite action `.github/actions/tofu-drift-gate/action.yaml` runs `tofu
  plan -detailed-exitcode -lock=false` against the GCS remote state. Exit `0` →
  pass; exit `2` (drift) and any other non-zero (plan error) → fail closed with
  an actionable message.
- **Scope is `google_*` only.** The github rulesets are passed to `-exclude` (a
  no-op when absent from state, belt-and-suspenders for refresh when present),
  and the action **never passes `github_repo_owner` / `github_repo_name` /
  `github_token`**, keeping the github provider anonymous + offline so the plan
  is token-free. `-exclude` requires OpenTofu ≥ 1.9 (the module pins ≥ 1.11).
- A new `drift-gate` job runs **after `infra`** (`needs: [gate-check,
  check-changes, infra]`, `always()` so the `infra=skipped` path still gates).
  Placing it after `infra` avoids false-positives on changes `infra` is about to
  apply; its real value is the `infra=skipped` path. The `deploy` job adds
  `drift-gate` to `needs` and requires `needs.drift-gate.result == 'success'`
  (no `|| skipped`, so failure/cancel both block).
- The action receives **every `TF_VAR_*` the `infra` Apply job sets** as explicit
  inputs (composite actions do not inherit the caller's `env:`). A static parity
  check (`scripts/check-drift-gate-parity.sh`, run by `just lint` and exercised
  by `tests/unit/drift_gate_parity_test.go` under `go test`) asserts the
  `TF_VAR_*` set matches across the infra job, the drift-gate `with:`, and the
  action — a missing var would fall back to `""` and read as false-positive drift.

## Enforcement inventory

### Entry points
- **push routing auto-deploy** — push classified `routing` → `gate-check.proceed
  =true` → `infra` (if `tofu/` changed) → `drift-gate` → `deploy`.
- **workflow_dispatch deploy** — operator dispatch whose `declared_category`
  matches the release-gate classification → same downstream DAG.
- (Not an entry point by design: `auth_boundary` / `schema` pushes set
  `proceed=false`, so `infra` / `drift-gate` / `deploy` never run on push; those
  deploy only via workflow_dispatch.)

### Persistent / carried data needed at each enforcement point
- WIF: `GCP_WORKLOAD_IDENTITY_PROVIDER` + `GCP_SERVICE_ACCOUNT` (`id-token:
  write`) to read GCS state and refresh live `google_*` infra.
- GCS backend pointer: `TOFU_STATE_BUCKET` + the fixed `prefix =
  runops-gateway/state`.
- The full `TF_VAR_*` set that defines desired state (must equal the `infra`
  set, minus the github provider vars).
- `deploy_sha` from `gate-check` — the gate checks out the same ref every other
  job ships.

### Bypass candidates ("where can this go wrong?")
- **infra-skip path** — when `tofu/` is unchanged, `infra` is `skipped`; this is
  the gate's primary useful path. Were `always()` dropped, `drift-gate` would
  skip and `deploy` would proceed ungated. Mitigated by `always()` +
  `(infra.result==success||skipped)` and `deploy` requiring `drift-gate.result
  =='success'`.
- **excluded github resources** — drift in the rulesets (or any future
  `github_*`) is invisible by construction. Accepted: ruleset integrity is
  enforced separately (release-gate / ADR 0031), and refreshing them needs a
  token absent in this context.
- **single-job re-run** — re-running `deploy` alone could skip a fresh
  `drift-gate`. Operators must "re-run all jobs"; documented in the failure
  message.
- **direct gcloud/console change** — cannot be blocked in real time. The gate is
  **detective at deploy time, not preventive** against out-of-band mutation; it
  catches such drift on the next pipeline run.
- **TF_VAR divergence across the 3 layers** (`infra` → `drift-gate with:` →
  action input) — a var missing in any layer falls back to `""` and reads as
  false-positive drift, blocking every deploy. Mitigated by
  `scripts/check-drift-gate-parity.sh` (in `just lint` + `go test`).
- **`github_repo_owner` leakage** — wiring it into the action would make the
  github provider go online and fail the token-free plan (fail closed → blocks
  deploy). The action does not declare an owner input, making this structurally
  impossible.

### Tests proving coverage
- `tests/unit/drift_gate_parity_test.go`: passes on the real files; fails when a
  `TF_VAR` is dropped from the action or from the drift-gate `with:` (proves the
  parity guard catches the divergence bypass).
- `just lint` runs `scripts/check-drift-gate-parity.sh` against the live files.
- GHA job-graph logic (`always()` + skipped-needs) is validatable only by
  `actionlint` (static) and a real `workflow_dispatch` dry-run (dynamic); there
  is no GHA unit harness, so this is verified out-of-band, not by an automated
  test.

## Consequences

### Positive
- A deploy can no longer ship on top of un-applied or manually mutated `google_*`
  infra; the gap is closed at the `infra=skipped` path that previously let drift
  through.
- Token-free plan works in the current production state where the GitHub App
  private key is absent.
- Parity is mechanically enforced, so the "false-positive drift blocks all
  deploys" failure mode is caught in CI, not in production.

### Negative
- When `tofu/` changed, `infra` already applied, so `drift-gate` is effectively a
  no-op confirming convergence — extra CI minutes for little signal on that path.
- `github_*` drift is out of scope; a separate mechanism must cover it.
- Every new `TF_VAR` on the `infra` job must be wired through three places (infra,
  drift-gate `with:`, action input) or the parity check fails.

### Neutral
- The gate is detective, not preventive: out-of-band changes are caught on the
  next run, not blocked at mutation time.
- The CI integration rides on `go test` (parity) + `just lint`; no dedicated
  `ci.yaml` step was added because `go test ./...` already exercises the parity
  test.
