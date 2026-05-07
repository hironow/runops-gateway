# 0033. Release-gate path list externalization (amends ADR 0031)

**Date:** 2026-05-08
**Status:** Proposed
**Amends:** ADR 0031 (production deploy gate on develop branch)

## Context

ADR 0031 introduced a CI-enforced release-gate keyed on three change categories (`routing` / `auth-boundary` / `schema`) and listed the paths that auto-classify a PR as `auth-boundary`. The `auth-boundary` path list was embedded inline in the ADR body, including precedents like `internal/usecase/port/*token*` that were inferred from a draft architectural plan rather than from the actual gateway source tree.

When implementing the release-gate workflow for the Phase β token broker (refs#0007), three issues surfaced:

1. The actual gateway architecture uses `internal/core/port/` and `internal/usecase/`, not `internal/usecase/port/`. The path list as written would not detect token broker code as `auth-boundary`.
2. Adding new auth-boundary surfaces over time (token broker handler under `internal/adapter/input/broker/**`, Cloud Run IAM verifiers under `internal/adapter/output/auth/**`, GitHub App ruleset IaC under `tofu/github_repository.tf`) requires editing ADR 0031 every time. ADRs in this project are treated as immutable per the project guideline (see `CLAUDE.md` adr-guidelines), so editing the canonical path list inside ADR 0031 is a process violation.
3. ADR 0031 itself is in the meta-gate path list (its own protections cover changes to the ADR). Editing the ADR is therefore an `auth-boundary` change that goes through the gate it documents — workable, but it conflates "evolving the path list" with "weakening the gate" in audit logs.

We need a way to evolve the path list as the source tree grows without rewriting ADR 0031 every time, while keeping the gate's audit trail clean.

A second concern, surfaced by the gpt-5.5 plan review of refs#0007 (2026-05-08), is that the `auth-boundary` policy implicitly covered "the GitHub App + Secret Manager broker" but did not name the specific paths. The token broker plan (`refs/plans/2026-05-08-issue-0007-token-broker-endpoint.md`) requires the gate to correctly classify changes touching `internal/core/port/*token*`, `internal/adapter/input/broker/**`, `internal/adapter/output/auth/**`, `internal/adapter/output/cache/**`, `internal/adapter/output/registry/agent_session_registry*`, `internal/adapter/output/github/**`, and `cmd/server/main.go` broker mount sections. ADR 0031 as written does not list these.

## Decision

Externalize the release-gate path list from ADR 0031 into a canonical YAML file that the release-gate workflow consumes directly.

### Canonical path list location

`.github/release-gate-paths.yaml` (new file). ADR 0031 references it as the canonical source.

**Critical: read from base ref, not PR head.** The release-gate workflow MUST read the path list from the protected base branch (`github.event.pull_request.base.sha`), not from the PR head. Reading from PR head would let an attacking PR delete entries from the YAML in the same commit and reclassify itself as `routing`. The base-ref read closes that escape hatch: the PR's own diff cannot influence which paths the classifier treats as `auth_boundary`.

**Hard-coded minimum self-protection set in the workflow.** Even if the YAML is missing, malformed, or somehow read incorrectly, the workflow contains an inline allow-list of paths that are unconditionally `auth_boundary`. The set is intentionally narrow — it covers the gate's own implementation:

- `.github/release-gate-paths.yaml`
- `.github/workflows/release-gate.yaml`
- `.github/workflows/cd.yaml`
- `.github/CODEOWNERS`

If a PR touches any of these paths, it is `auth_boundary` regardless of YAML content. The hard-coded set is reviewed identically to the gate's other code (see meta-gate). Any path list change PR is therefore `auth_boundary` "by content of the diff" via the hard-coded set, not by reading the (potentially-tampered) YAML the PR introduces.

**Rename / delete / chmod detection.** "Touch" includes rename and delete, not just content edit. The classifier inspects every PR file with **both the current path and the previous path**, treating either as a match against the hard-coded set or the YAML globs:

- When using the GitHub Pull Request files API, match on both `filename` and `previous_filename`.
- When using `git diff`, run `git diff --name-status` (or `--diff-filter=ACMRDT`) so rename (`R`) and delete (`D`) entries surface their previous paths; do not rely on `--name-only -M` alone.
- Rename, delete, mode change (chmod), file truncation, and content edit on any path in the hard-coded set are all `auth_boundary`. A PR that renames `.github/CODEOWNERS` to `.github/CODEOWNERS.bak` is `auth_boundary` because the previous path is in the set; a PR that deletes `.github/workflows/release-gate.yaml` is `auth_boundary` for the same reason.

This closes the rename-bypass corner: an attacker cannot rename gate files to escape the meta-gate, then re-add them under a different name in a follow-up `routing` PR.

Schema:

```yaml
# .github/release-gate-paths.yaml
# Canonical release-gate path classification (referenced by ADR 0031,
# externalized by ADR 0033). Edits to this file are themselves
# auth-boundary changes (see meta-gate).
auth_boundary:
  # Broker code (token mint, caller auth, agent session registry, cache)
  - "internal/core/port/*token*"
  - "internal/usecase/broker_token*"
  - "internal/adapter/input/broker/**"
  - "internal/adapter/output/auth/**"
  - "internal/adapter/output/cache/**"
  - "internal/adapter/output/registry/agent_session_registry*"
  - "internal/adapter/output/github/**"
  # Composition root: any change to mount/unmount of broker handlers,
  # auth middleware ordering, or DI of the broker port lands here.
  - "cmd/server/**"
  # IaC for IAM, Secret Manager, GitHub provider, Repository Ruleset.
  # Existing layout puts deployer IAM in tofu/github.tf and Secret
  # Manager resources/accessors in tofu/main.tf, so the gate is
  # fail-closed: every .tf file under tofu/ is auth-boundary.
  - "tofu/**/*.tf"
  - "tofu/**/*.tfvars"
  # Workflow + gate self-defence (meta-gate).
  - ".github/workflows/release-gate.yaml"
  - ".github/workflows/cd.yaml"
  - ".github/workflows/deploy*.yaml"
  - ".github/CODEOWNERS"
  - ".github/release-gate-paths.yaml"
  - ".semgrep/rules/release-gate/**"
  - "docs/adr/0031-*"
  - "docs/adr/0032-*"
  - "docs/adr/0033-*"
schema:
  - "internal/core/domain/dmail*.go"
  - "internal/core/domain/event*.go"
  - "internal/adapter/output/firestore/**"
  - "internal/core/port/*.go"
# `routing` is the implicit fallback: any PR whose changed files do
# not match auth_boundary or schema globs is classified `routing`.
```

Any path under `auth_boundary` or `schema` classifies a PR up to the strictest matching category. Globs use the same conventions as `gh api` and GitHub Actions `paths` filters (`**` matches across directories, `*` matches within one).

### Meta-gate self-protection

The path list YAML is itself listed under `auth_boundary` (`.github/release-gate-paths.yaml`), and is also covered by the hard-coded self-protection set above. Any PR that edits the path list is auto-classified `auth-boundary` regardless of the YAML's post-edit content, requires a second human approver, and cannot bypass the gate it defines. ADR 0031's meta-gate reasoning (a gate that can be silently weakened by a PR is no gate) carries over: this YAML file is treated identically to the workflow files and IaC files that implement the gate.

The combination of base-ref reads + hard-coded set means: even if an attacker submits a PR that simultaneously deletes entries from `release-gate-paths.yaml`, deletes `.github/workflows/release-gate.yaml`, and renames `.github/CODEOWNERS`, the classifier still sees the changes as `auth_boundary` because (a) the YAML it reads is the *base* version (still listing those paths), and (b) the hard-coded set inside the workflow flags the PR independently of YAML content.

### `actions/create-github-app-token` SHA pin policy

ADR 0031 declared that release-gate / ruleset / deploy workflow files are auth-boundary. ADR 0033 adds a corollary: any third-party GitHub Action invoked from those workflows that mints, holds, or forwards GitHub-side credentials (e.g., `actions/create-github-app-token`, `google-github-actions/auth`, `aws-actions/configure-aws-credentials`) **must** be pinned to a full commit SHA, not a tag or major version. Pinning to a moving tag would re-introduce the supply-chain bypass that the meta-gate exists to prevent.

The release-gate workflow's own Semgrep ruleset (`.semgrep/rules/release-gate/**`) enforces this: any usage line `uses: <action>@<ref>` inside `.github/workflows/cd.yaml` or `.github/workflows/release-gate.yaml` whose `<ref>` is not 40 hex chars is flagged ERROR.

### GitHub App for Repository Ruleset management

ADR 0031 said the `prod-*` tag protection ruleset is managed via IaC. ADR 0033 names the credential path: a dedicated GitHub App, `runops-gateway-release-gate` (working name), with `Administration: Read & Write` and `Repository contents: Read` on the `hironow/runops-gateway` repository. Its private key is stored in GitHub Actions secret `RELEASE_GATE_APP_PRIVATE_KEY`; its app ID in repo variable `RELEASE_GATE_APP_ID`.

The `cd.yaml` infra-apply job mints an installation token via `actions/create-github-app-token@<commit-SHA>` and exports it as `TF_VAR_github_token` so the OpenTofu GitHub provider can apply the ruleset / branch protection / tag protection.

A break-glass PAT may exist as an emergency override (kept in a separate, sealed secret, used only via a `workflow_dispatch` job that has its own audit log entry); rotation, holder, and use-case for the PAT are documented in this ADR as a follow-up entry when the PAT is provisioned.

## Consequences

### Positive

- The release-gate path list can evolve without amending ADR 0031, keeping ADR 0031 as a stable Decision document
- The token broker (refs#0007) and any future auth-boundary feature can extend the path list via a normal PR (gated as auth-boundary itself)
- Audit trail separates "gate evolution" (path list edits) from "gate weakening attempts" (workflow / Semgrep / CODEOWNERS edits) cleanly
- Third-party action SHA pin policy closes the supply-chain corner of the meta-gate that ADR 0031 left implicit

### Negative

- One more file (`.github/release-gate-paths.yaml`) for an operator to mentally track; mitigated by the file being short and self-documenting
- `tofu/**/*.tf` in `auth_boundary` is intentionally over-broad: it catches Google provider version bumps and unrelated routing-only IaC tweaks. The trade-off is acceptable because the existing repo layout puts high-privilege deployer IAM in `tofu/github.tf` and Secret Manager resources in `tofu/main.tf`, so a narrower glob would let production-affecting changes slip through as `routing`. Fail-closed beats fail-open for the gate's primary purpose. Future refactors that move IAM and secrets into clearly-named files (`tofu/iam_*.tf`, `tofu/*secret*.tf`) can revisit the glob via a new ADR amending this one

### Neutral

- ADR 0031 remains the canonical statement of the gate's existence and intent; ADR 0033 is purely a process-mechanics amendment
- The path list does not retroactively reclassify merged PRs; it applies forward-going

## References

- ADR 0031 — production deploy gate on develop branch (the gate this amends)
- gpt-5.5 codex review of refs#0007 token broker plan (2026-05-08)
- `refs/plans/2026-05-08-issue-0007-token-broker-endpoint.md` — Phase 0 implementation that consumes this externalization
- `refs/docs/issues/0007-runops-gateway-github-app-secret-manager.md` — the auth-boundary feature this enables
- `CLAUDE.md` adr-guidelines — ADR immutability rule that drove the externalization choice
