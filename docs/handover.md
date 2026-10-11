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
Three Takumi Guard steps still need removal from CI and release-gate.

## Next Actions

1. Remove the Guard steps and keep release-gate classification unchanged.
2. Run the local gate, then create a ready PR targeting develop.
3. Verify every CI check, squash merge, and remove the Flatt Actions allowlist entry.

## Known Risks / Blockers

Docker is unavailable locally.
CI must verify the testcontainers integration tests.
The release-gate workflow is in its own auth-boundary protection set.
Changing it must still produce auth_boundary classification.
