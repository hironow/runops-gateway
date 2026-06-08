# 0045. Drift radar notifies by failing the job (issues disabled)

**Date:** 2026-06-08
**Status:** Accepted

Supersedes [0044](0044-scheduled-drift-radar.md).

## Context

ADR 0044 implemented the scheduled drift radar to **open a GitHub issue** when
`google_*` drift is detected, chosen because the repo has no Actions secrets
(`gh secret list` is empty), so the built-in `GITHUB_TOKEN` + an issue avoided
introducing a Slack webhook secret.

That decision was based on a wrong premise. `gh repo view --json hasIssuesEnabled`
returns **false** — this repository has its Issues feature disabled. So
`gh issue create` fails with "repository has disabled issues", and because the
radar step runs `set -euo pipefail`, the radar job would fail *only on the drift
path* (`if: drift == 'true'`). The notification mechanism breaks exactly when it
is needed, and the bug stays hidden until real drift occurs. The radar had not
yet run (neither schedule nor dispatch), so no incident resulted.

## Decision

The radar surfaces drift by **failing the job**, not by opening an issue.

- The radar runs the `tofu-drift-gate` composite action in its normal (gate) mode:
  drift → the action `exit 1` → the radar job fails → GitHub's **workflow failure
  notification** is the signal. No issue, no `issues: write`, no secret.
- Because both the deploy-time gate (cd.yaml) and the radar now signal drift by
  failing, the action's **report mode is removed**: the `fail_on_drift` input and
  the `drift` output added in ADR 0044 became unused API and are deleted, returning
  the action to a single gate-only behaviour. (If a non-failing report path is
  needed later — e.g. a Slack notification once a webhook secret exists — report
  mode can be reintroduced then.)
- The radar's daily schedule (`17 18 * * *` UTC) + `workflow_dispatch` and the
  `google_*`-only token-free plan (ADR 0043/0044) are unchanged.

## Enforcement inventory

Invariant: *"google_* drift is surfaced within one day even without a deploy."*

### Entry points
- `schedule` (daily) and `workflow_dispatch` on `drift-detect.yaml`.

### Persistent / carried data needed at each enforcement point
- WIF (`GCP_WORKLOAD_IDENTITY_PROVIDER` + `GCP_SERVICE_ACCOUNT`, `id-token: write`)
  and `TOFU_STATE_BUCKET` — to read state and refresh live `google_*` infra.
- The full `TF_VAR_*` set (equal to the infra set, minus `github_*`).
- No `issues: write` and no `GITHUB_TOKEN` issue access are needed any more.

### Bypass candidates ("where can this go wrong?")
- **silent radar stop** — a disabled schedule / deleted workflow goes unnoticed
  (no failure = no news). Known residual risk; a heartbeat is out of scope.
- **failure notification not delivered** — relies on each maintainer's GitHub
  workflow-failure notification settings; there is no issue to track state.
  Accepted as the consequence of the issues-disabled repo posture.
- **4-layer TF_VAR divergence** — a var missing in the radar `with:` would file a
  false-positive failure daily. Mitigated by `scripts/check-drift-gate-parity.sh`
  (infra / drift-gate / drift-detect / action).
- **schedule branch mismatch** — `schedule` fires on the default branch's workflow
  only. The default branch is `develop` (verified via
  `gh repo view --json defaultBranchRef`), where this workflow lives, so the cron
  is effective once merged to develop.

### Tests proving coverage
- `tests/unit/drift_gate_parity_test.go` (4-layer parity, fails on a dropped var).
- `actionlint` over `drift-detect.yaml`.
- Behaviour validation is dispatch-only after merge: a `workflow_dispatch` run with
  no drift must succeed; the drift→fail path is validated with a fixture / forced
  drift, never by mutating production infra.

## Consequences

### Positive
- Drift is reliably surfaced in an issues-disabled repo; the broken issue path is
  removed.
- The action is simpler (single gate-only behaviour); no unused report-mode API.
- No new secret and no repo-settings change required.

### Negative
- No issue-based tracking of an open drift condition; the signal is a red
  scheduled run plus GitHub's failure notification.
- Re-adding a non-failing notification (Slack, etc.) later means reintroducing a
  report path in the action.

### Neutral
- The radar remains detective, not preventive (ADR 0043/0044 unchanged on that).
- A daily failed run is the steady-state signal while drift persists, until a
  human reconciles state.
