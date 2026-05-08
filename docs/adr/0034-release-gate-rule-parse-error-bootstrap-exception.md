# 0034. Release-gate rule parse error bootstrap exception

**Date:** 2026-05-08
**Status:** Proposed

## Context

ADR 0033 §"Canonical path list location (base-ref read)" pinned the
release-gate rule sourcing strategy:

> The Semgrep rule under `.semgrep/rules/release-gate/category-auth-boundary.yaml`
> is read from the PR's BASE ref (`git show "${BASE_SHA}:..."`),
> not the PR head, so a malicious PR cannot rewrite the rule
> file in the same commit to bypass the gate.

This is correct for the threat model "PR-level rule mutation". It
also has a hidden failure mode that surfaced during the token
broker (refs#0007) Phase 0 rollout (PR #53 → PR #55, 2026-05-07):

1. Phase 0 (PR #53) merged the release-gate workflow + rules into
   develop.
2. The merged rule contained an invalid Semgrep pattern
   (`go-github.NewClient($T)` — hyphen in identifier, syntactically
   invalid Go). The Semgrep parser rejects the rule with a
   `Rule parse error` and exits with code 2.
3. release-gate.yaml's "Refusing to fall back to path-only
   classification (fail-closed)" guard treats exit ≥2 as a hard
   fail.
4. Every subsequent PR — including PR #55 which contained the
   one-line fix to the broken rule — failed the release-gate
   commit status, because the BASE ref still pointed at the
   broken rule.

The only escape was a one-time `gh pr merge 55 --squash` UI bypass
documented in the PR comment trail. This works because:

- `develop` branch protection at the time required only
  `non_fast_forward` and `deletion` rules; the release-gate
  commit status was not a `required` check at the GitHub
  Repository Ruleset level.
- The fix landed on develop immediately after, and every later PR
  had a healthy BASE ref again.

But the bypass left an unresolved architectural concern: in a
strict-required-check world (= future hardening of the develop
ruleset), a future broken rule would create a permanent
deadlock. ADR 0033's design did not anticipate this self-fix
path.

## Decision

Add a narrowly-scoped bootstrap exception to the release-gate
workflow's Semgrep step, conditioned on three simultaneous
predicates:

1. The Semgrep run exited with a Rule parse error (= the BASE
   ref's rule file is corrupt).
2. The PR's changed-paths set is **exclusively** under
   `.semgrep/rules/release-gate/**` and contains at least one
   file that touches `category-auth-boundary.yaml` or
   `gateway-broker-token-leak.yaml`.
3. The PR's changed-paths set does NOT contain any file from the
   hard-coded self-protection set in release-gate.yaml itself
   (= `.github/workflows/release-gate.yaml`, `.github/workflows/cd.yaml`,
   `.github/release-gate-paths.yaml`, `.github/CODEOWNERS`).

When all three predicates are satisfied:

- The classifier publishes the commit status with
  `category=auth_boundary`, `reason=rule self-fix bootstrap exception`.
- The Semgrep ERROR-severity guard does NOT fire (= the workflow
  passes despite the rule parse error).
- The hard-coded self-protection set match still produces the
  auth_boundary escalation; the bootstrap exception only relaxes
  the parse-error fail-closed branch.

When any predicate fails:

- The existing fail-closed behaviour from ADR 0033 is preserved.
  A PR that touches release-gate workflow files, or mixes rule
  fixes with other auth-boundary changes, OR contains rule fixes
  but no actual rule-shape change, is still rejected.

The implementation lands in a separate PR after this ADR is
Accepted; the change touches `.github/workflows/release-gate.yaml`
only and is auth_boundary by hard-coded set match.

## Consequences

### Positive

- Rule corruption is recoverable in a single PR without manual
  GitHub UI bypass.
- The threat model "PR rewrites rule to bypass gate" remains
  fully covered: predicate (1) requires the BASE rule to be
  parse-error-broken in the first place, which would have
  required a malicious PR to land FIRST in a state that
  release-gate could not have approved.

### Negative

- Predicate (2) introduces a small additional code path in
  release-gate.yaml. Workflow complexity grows.
- A future operator might add a rule file under
  `.semgrep/rules/release-gate/` that intentionally trips a
  Semgrep parse error to invoke the bootstrap path for an
  unrelated reason. Mitigation: the predicate also requires the
  PR's changed-paths to exclusively touch the rule directory, so
  such an attempt would be visible in code review and the
  "no other auth_boundary path" guard catches it.

### Neutral

- ADR 0033's BASE-ref-read invariant is preserved unchanged. The
  bootstrap exception is layered on top, not a replacement.
- The hard-coded self-protection set in release-gate.yaml is
  unchanged; CODEOWNERS / paths.yaml / workflow file edits still
  go through the meta-gate two-reviewer path.
- A future amendment ADR could shrink the bootstrap exception to
  zero predicates (= remove this) once the team is comfortable
  with `git revert` workflows on develop's rule files. The ADR
  would then supersede this one with `Status: Superseded by [NNNN]`.
