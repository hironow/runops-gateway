# 0031. Production deploy gate on develop branch

**Date:** 2026-05-08
**Status:** Accepted

## Context

Production runops-gateway runs on the `develop` branch. The decision to keep production on `develop` (rather than promoting through `main`) was made earlier in the multiplex Phase α work to keep iteration velocity high while routing-only changes were being shipped (`#0006`-`#0012`, all merged into `develop` 2026-05-07).

This was acceptable for routing changes because their blast radius is bounded:

- A bad receiver routing change misroutes D-Mails (recoverable by re-emit)
- A bad emitter attribute change drops `project_id` on outbound messages (recoverable by re-publish)
- A bad Slack `--project=<id>` parser change rejects valid input (no data corruption)

The forthcoming Phase β work breaks this assumption. `refs#0007` (GitHub App + Secret Manager token broker endpoint) introduces GitHub installation token minting from inside the gateway. Token minting changes the blast radius:

- A bad token broker can grant write tokens to the wrong actor
- A bad token broker can leak installation tokens through Pub/Sub / D-Mail / archive / logs
- A bad token broker can silently bypass the 4-eyes approval boundary (`refs#0011`) when AI agents and human operators become indistinguishable

The gpt-5.5 review of remaining multiplex issues (2026-05-08) flagged this as a fatal precondition: "production-on-develop is tolerable for routing, but token minting needs an explicit release gate before being deployed".

We need a release gate that closes the gap before any token broker code lands on `develop`, without forcing an immediate `main` promotion of all in-flight Phase α work.

## Decision

Adopt a tiered release gate keyed on **change category**, enforced **mechanically** by CI (not by self-declaration), with a single, unambiguous deploy-pipeline ref semantics.

### Change categories (CI-enforced)

Every PR targeting `develop` is assigned a category by **CI path-detection**, not by author self-declaration. Self-declared category in PR body is used only to confirm or override the auto-detected category, and any override of `auth-boundary`/`schema` to `routing` requires a second human approver.

Categories:

1. **routing** — auto-assigned when no `auth-boundary` and no `schema` paths are touched. Slack command parsing, D-Mail receiver routing, emitter attribute setting, project registry CRUD outside IAM/Secret Manager. Continues to deploy on `develop` push as today.
2. **auth-boundary** — auto-assigned when any of these paths/strings appear in the PR diff:
   - `tofu/**/*iam*` / `tofu/**/*secret*` / `tofu/**/*github_app*`
   - `**/secretmanager*` / `**/installation*token*` / `**/github_app*`
   - any new `Environment=` containing `TOKEN`/`SECRET`/`KEY`/`PRIVATE` in the diff
   - any change to `internal/adapter/output/auth/**` or `internal/usecase/port/*token*` (paths reserved for the future broker)
   - any change to GitHub App manifest / installation permission JSON
3. **schema** — auto-assigned when any of these paths appear:
   - `internal/domain/dmail*.go` (D-Mail schema)
   - `internal/domain/event*.go` (event schema)
   - `internal/adapter/output/firestore/*.go` schema-touching file (excluding read-only adapters)
   - `internal/usecase/port/*.go` exported method signature change

CI detection is a Semgrep ruleset under `.semgrep/rules/release-gate/` plus a path-glob check in `.github/workflows/release-gate.yaml`. Both must agree; either alone classifies the PR up to the stricter category.

### Release gate for `auth-boundary` and `schema`

Before merging to `develop`:

- ADR documenting the decision and blast radius (Accepted state)
- Rollback plan written into the PR description: which deploy ref to revert to, how long it takes, what state requires manual recovery
- A second human reviewer approval (the CI category check is required to be green, but a second reviewer is the human escalation when the auto-detection itself is contested)

Before deploying to production:

- Deploy ref recorded in `docs/deploy-log.md` (new file, append-only)
- For `auth-boundary` deploys: pre-deploy verification against staging (Cloud Run revision + traffic-split 0%) + smoke test
- For `schema` deploys: forward/backward compatibility note in the deploy log

### Deploy pipeline ref semantics (single mode, no ambiguity)

The previous draft mixed "merge to develop = production candidate" with "deploy ref set explicitly". That dual mode is the bug. We adopt one mode:

- **`develop` push pipeline runs only on `routing` PRs**. The `release-gate` workflow inspects the merged commit, and if the resulting category is `auth-boundary` or `schema`, the production deploy job is **skipped** (not failed; the build continues so feature work is not blocked, but the deploy step is gated). A status check records the skip with reason `gated by ADR 0031`.
- **`auth-boundary` and `schema` production deploys use `workflow_dispatch` only**. The operator dispatches the deploy workflow against an explicit ref (`main` after promotion, or a release branch). No automatic deploy on `develop` push for these categories.
- The deploy job reads the category off the dispatch input and refuses to deploy if it does not match the actual diff between current production and the requested ref.

### Meta-gate: protecting the gate itself

A release gate that can be silently weakened by a PR that touches the gate's own implementation defeats the purpose. The gate is itself an `auth-boundary` artifact. To prevent that escape hatch:

- The following paths are **always classified `auth-boundary`** by the auto-detector, regardless of any other diff in the PR:
    - `.github/workflows/release-gate.yaml`
    - `.github/workflows/deploy*.yaml` (any deploy workflow)
    - `.semgrep/rules/release-gate/**`
    - `.github/CODEOWNERS` lines covering any of the above
    - `docs/adr/0031-production-deploy-gate-on-develop.md` (this document — superseding requires a new ADR)
    - **GitHub repository ruleset / branch protection / tag protection IaC**: `tofu/github_repository.tf` and any other Terraform / OpenTofu file declaring `github_repository_ruleset`, `github_branch_protection`, or `github_repository_tag_protection` resources. Without this, a PR that softens the `prod-*` tag protection ruleset can be classified as `routing` and bypass the gate entirely.
- Branch protection on `develop` requires the `release-gate` status check to pass; that check itself is implemented under `.github/workflows/release-gate.yaml`, so the loop closes only because the path is in the auto-`auth-boundary` set above and changes there require a second human approver.
- `CODEOWNERS` for the meta-gate paths must list at least two reviewers; a single reviewer cannot self-approve a gate-weakening PR.

### Rollback target separation (rollback ref ≠ promotion candidate)

`main` is a **promotion candidate**, not the rollback target. The previous draft conflated the two: when `main` is fast-forwarded so an `auth-boundary` deploy can use it as its source ref, `main` no longer points at the previously-deployed-and-known-good production state. Treating `main` as both leaves the system without a clean rollback ref the moment promotion happens.

We separate them:

- **Promotion candidate** = `main`. Fast-forwarded from `develop` only when zero in-flight `auth-boundary`/`schema` PRs are present. `auth-boundary` and `schema` production deploys consume refs reachable from this `main`.
- **Rollback target** = the **immutable production tag** `prod-<YYYYMMDD>-<sha7>` cut against the previous successful production deploy commit. Deploy workflow creates this tag automatically before each successful production cutover. The Cloud Run revision label `prod-<sha7>` is set to the same value.
- **Rollback procedure** = re-deploy the most recent `prod-*` tag whose Cloud Run revision is still healthy in revision history (Cloud Run keeps revisions for 90 days by default). The operator does **not** rollback by reverting `main`; rolling back `main` would create the same conflation problem.
- `docs/deploy-log.md` records both the new `prod-*` tag and the previous one (so the rollback target is one entry above in the file).

#### `prod-*` tag immutability (rollback ledger integrity)

A rollback ledger that allows force-push, delete, or re-creation is not a ledger. The `prod-*` tag namespace is protected by these rules, enforced at the GitHub repository level:

- **Tag protection rule**: GitHub Repository Ruleset matching the pattern `prod-*` denies `delete` and `force-push` to anyone (including admins). The rule cannot be bypassed even with admin override; it has to be removed deliberately, and removing it is itself an `auth-boundary` change (the ruleset IaC paths are listed in the meta-gate above so any softening PR is auto-classified `auth-boundary`).
- **Tag creation authority**: the same Repository Ruleset restricts `create` on `prod-*` tags to the deploy workflow's identity (a scoped GitHub App or deploy-key-equivalent token). Human operators cannot push `prod-*` tags from their workstations not because of policy alone, but because the GitHub Ruleset rejects the push at the API layer.
- **Idempotency on retry, fail on conflict**: the deploy workflow's tag-creation step is **retry-safe**:
    - If the target `prod-<YYYYMMDD>-<sha7>` tag does **not** exist, create it pointing at the expected previous-production commit and proceed.
    - If the tag **exists and points at the expected previous-production commit**, treat as success (re-entering after a partial failure between tag creation and Cloud Run cutover) and proceed.
    - If the tag **exists but points at a different commit**, fail loudly and require operator inspection. This catches accidental same-day same-sha clashes (extremely unlikely) and any external tampering before the protection rule was applied.
    - The "expected previous-production commit" is read from the immutable Cloud Run revision label on the currently-serving revision, not from `docs/deploy-log.md`. The ledger is auxiliary and could be edited; the revision label cannot.
- **SoT for rollback** = protected `prod-*` tag (immutable) + Cloud Run revision label `prod-<sha7>` (immutable on the revision). `docs/deploy-log.md` is an auxiliary human-readable ledger; if the log and the tags ever disagree, the protected tags + revision labels win.
- The above rules are themselves part of the meta-gate (see "Meta-gate: protecting the gate itself"); changes to the tag protection ruleset, the deploy workflow's tag-creation step, or the IaC defining either are auto-classified `auth-boundary`.

### `main` promotion path (single rule)

- For `auth-boundary` / `schema` work, the deploy ref is **always a `main`-promoted commit** (or a release branch cut from such a commit).
- `main` is fast-forwarded from `develop` only when `develop` contains zero in-flight `auth-boundary`/`schema` PRs (the auto-detection above checks this). Operators do not rebase `main` over divergent history.
- Token broker (`refs#0007`) and AI agent identity (`refs#0011`) deploy refs **must** be reachable from `main` at deploy time. The auto-detector blocks the production deploy job otherwise.
- After a successful production deploy, no `develop`-back-merge is required: `develop` already contains the work (it was merged there first), and `main` simply caught up to a promotion-eligible commit.
- **Rollback** does not touch `main`; it re-deploys the previous `prod-*` tag (see "Rollback target separation" above).

This eliminates the earlier "rebase main onto develop" vs "promote main first" ambiguity. There is one rule: `main` represents the latest known-good promotion candidate, `prod-*` tags are the rollback ledger, and `auth-boundary`/`schema` deploys consume only refs reachable from `main`.

## Consequences

### Positive

- Token broker and identity work cannot land on `develop` without a documented release gate, eliminating the blast-radius gap noted in the gpt-5.5 review
- Routing-category PRs keep their existing fast deploy cadence
- Operators have a written rollback ref for any `auth-boundary` deploy, instead of relying on memory of "what was on develop before"
- `main` remains a known-good promotion target; future bisecting and external integrations can pin against `main` without sliding behind active development

### Negative

- Authors of `auth-boundary` PRs need to write rollback plans inline; this is friction for small token-related fixes. The friction is intentional — token-related fixes have outsized blast radius
- Categorising PRs adds a small triage cost. Categorisation can be enforced lightly via PR template

### Neutral

- The `routing` category continues to behave like the current `develop`-as-production model. Most PRs will stay in this category
- ADR 0031 does not retroactively re-categorise the merged Phase α PRs (#0006-#0012); they were correctly classified as `routing` and are already in production
- `docs/deploy-log.md` is a new operational artifact. Its format is intentionally simple (plaintext append-only) to avoid tooling lock-in

## References

- gpt-5.5 codex review of remaining multiplex issues (`refs/docs/issues/README.md` 推奨着手順 + Phase β prerequisite, 2026-05-08)
- ADR 0023 — dmail-daemon OCI image deployment (deploy mechanics)
- ADR 0026 — Firestore production deploy (in-flight `develop` deploy precedent)
- ADR 0030 — HTTP admin endpoint authentication (auth-boundary precedent on routing changes)
- `refs/docs/issues/0007-runops-gateway-github-app-secret-manager.md` (token broker, blocked by this ADR)
- `refs/docs/issues/0011-runops-gateway-ai-agent-identity-4-eyes.md` (4-eyes, blocked by this ADR)
