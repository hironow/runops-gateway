# 0044. Scheduled drift radar

**Date:** 2026-06-02
**Status:** Superseded by [0045](0045-drift-radar-job-fail-notification.md)

## Context

ADR 0043 added a deploy-time drift gate. It is **detective at deploy time only**:
it runs as part of `cd.yaml`, so drift is caught at most once per deploy. This
repo deploys infrequently, so a manual gcloud/console change or an un-applied
`tofu/` change can sit undetected for a long time between deploys. ADR 0043
itself notes this: its useful path is the `infra=skipped` branch, but it still
only fires when a deploy is attempted.

We want to catch drift **without waiting for a deploy** — on a schedule and on
demand — and surface it where Ops can track it.

Two constraints from the repo:

- **No GitHub Actions secrets exist** (`gh secret list` is empty); CD runs on WIF
  + repo variables. A Slack Incoming Webhook would require introducing and
  operating a new secret. The built-in `GITHUB_TOKEN` can open issues with no
  secret, matching the repo's secret-less posture.
- The deploy-time gate is a composite action (ADR 0043), so the same plan logic
  can be reused if the action can run in a non-failing "report" mode.

## Decision

Add a **scheduled drift radar** that reuses the ADR 0043 composite action.

- **Action report mode.** `.github/actions/tofu-drift-gate/action.yaml` gains a
  `fail_on_drift` input (default `"true"`) and a top-level `drift` output.
  - `fail_on_drift=true` (gate, the cd.yaml default): drift → `exit 1` (unchanged
    ADR 0043 behaviour; cd.yaml does not set the input, so it is fully backward
    compatible).
  - `fail_on_drift=false` (radar): drift → `exit 0` and `drift=true` output.
  - Plan error → `exit 1` in both modes (fail closed).
- **Radar workflow.** `.github/workflows/drift-detect.yaml` runs on `schedule`
  (daily, `17 18 * * *` UTC ≈ 03:17 JST, off-peak/non-round) and
  `workflow_dispatch`. It calls the action in report mode and, when `drift=true`,
  opens a GitHub issue with the built-in `GITHUB_TOKEN`. The issue is
  **idempotent**: an existing open "Tofu drift detected" issue is commented on;
  otherwise a new one is opened, so daily runs do not pile up issues.
- **Notification = GitHub issue**, not Slack — no secret required.
- **Scope = `google_*` only**, same as ADR 0043 (token-free plan; `github_*`
  excluded).

## Enforcement inventory

Invariant: *"google_* drift is surfaced (issue) within one day even without a deploy."*

### Entry points
- `schedule` (daily cron) — unattended detection.
- `workflow_dispatch` — operator on-demand detection.

### Persistent / carried data needed at each enforcement point
- WIF: `GCP_WORKLOAD_IDENTITY_PROVIDER` + `GCP_SERVICE_ACCOUNT` (`id-token: write`).
- GCS backend pointer: `TOFU_STATE_BUCKET` + fixed `prefix=runops-gateway/state`.
- The full `TF_VAR_*` set (must equal the infra set, minus `github_*`) — same
  desired-state inputs the gate uses.
- `issues: write` permission + built-in `GITHUB_TOKEN` for issue creation.

### Bypass candidates ("where can this go wrong?")
- **report mode swallows drift** — if `fail_on_drift=false` also suppressed the
  signal, drift would go unnoticed. Mitigated: the `drift` output is set in every
  branch (`true`/`false`/`error`), and the issue step keys off `outputs.drift`.
- **issue spam** — daily runs could open one issue per day. Mitigated by the
  idempotent open-or-comment logic keyed on the issue title.
- **4-layer TF_VAR divergence** — radar adds a third `with:` site (after infra and
  the gate). A var missing here makes the radar file a false-positive issue daily.
  Mitigated by `scripts/check-drift-gate-parity.sh` now comparing four layers
  (infra / drift-gate `with:` / drift-detect `with:` / action env).
- **radar itself stops** — a disabled schedule or deleted workflow goes unnoticed
  (no news looks like good news). Known residual risk; a heartbeat ("alive, no
  drift") is out of scope for this iteration.
- **plan error** — a backend/auth failure must not be read as "no drift". The
  action emits `drift=error` and `exit 1`, so the radar job fails and GitHub's
  job-failure notification fires.

### Tests proving coverage
- `tests/unit/drift_gate_parity_test.go`: 4-layer parity, including a case that
  fails when the radar `with:` drops a var.
- `actionlint` over `drift-detect.yaml` (job graph + composite `run:` shellcheck).
- Real-run validation is dispatch-only: a production `workflow_dispatch` confirms
  "no drift → no issue". The drift→issue branch is validated with a fixture /
  temporary workflow that forces `drift=true`, never by mutating production infra.

## Consequences

### Positive
- Drift is caught daily (and on demand) without waiting for a deploy, closing the
  ADR 0043 "deploy-time only" gap.
- No new secret: issue notification uses the built-in token, consistent with the
  repo's secret-less CD.
- The composite action is now reusable in both gate and report modes; one source
  of truth for the plan logic.

### Negative
- Radar liveness is unmonitored (no heartbeat); a silently disabled schedule is
  not detected.
- A fourth parity layer must be kept in lockstep; any new infra `TF_VAR` must be
  wired into the radar `with:` too.
- Issues are not auto-closed on remediation; a human closes them.

### Neutral
- Detective, not preventive: the radar reports drift, it does not block the
  out-of-band change that caused it.
- Daily plan adds a small, steady WIF + GCP API call volume (one plan/day).
