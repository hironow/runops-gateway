# Handover

**Last updated:** 2026-10-11 (JST)
**Updated by:** Codex

## Current State

The isolated lint tool module uses golangci-lint v2.14.0.
Its Go importer supports the export-data format used by stable Go 1.27.
The application code, dependencies, lint rules, and deployment logic are unchanged.
`just check` passed with Go 1.27.2: lint and Semgrep found zero issues, 15 OpenTofu tests passed, drift-gate parity passed, and normal tests passed.
The Markdown formatter also repaired old documents; those edits were restored to preserve accepted ADRs and keep this change scoped.

## In Progress

Branch: `ci/remove-flatt`, based on develop.
The lint update is a separate preparatory commit.
Three Takumi Guard steps are removed from CI and release-gate.
The release-gate scanner updates Semgrep to 1.179.0 to avoid the removed pkg_resources dependency and uses official PyPI through uv.
The protected workflow pins its install command directly; it does not execute the PR head justfile while classifying changes.
Release-gate Python runs through uv with Python 3.12.
PR jobs in CI, release-gate, and CodeQL skip drafts and resume on ready_for_review.

## Next Actions

1. Run the local gate and review the diff.
2. Create a ready PR targeting develop.
3. Verify every CI check, squash merge, and remove the Flatt Actions allowlist entry.

## Known Risks / Blockers

Docker is unavailable locally.
CI must verify the testcontainers integration tests.
The release-gate workflow is in its own auth-boundary protection set.
Changing it must still produce auth_boundary classification.
