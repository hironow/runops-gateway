# https://just.systems

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# External commands
MARKDOWNLINT := "mise exec -- markdownlint-cli2"

# Default: show help
default: help

# Help: list available recipes
help:
    @just --list --unsorted


# Build all binaries
build:
    go build ./cmd/...

# Run the HTTP server
run:
    go run ./cmd/server

# Run the HTTP server with local-dev env loaded (.env.local, then optional .env
# overrides). Presumes the dotfiles stack is up: emu-up-only firebase-emulator + tel-up.
dev:
    #!/usr/bin/env bash
    set -euo pipefail
    set -a
    source .env.local
    [ -f .env ] && source .env
    set +a
    go run ./cmd/server

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-v:
    go test -v ./...

# Run all tests with the race detector (same flag as the CI test job)
test-race:
    go test -race ./...

# Run linting (go vet + golangci-lint + semgrep + tofu test)
lint:
    go vet ./...
    CGO_ENABLED=0 go tool -modfile=tools/go.mod golangci-lint run ./...
    just semgrep
    just test-iac
    just check-drift-gate-parity

# Check tofu drift-gate input parity (ADR 0043): the TF_VAR_* set must match
# across the cd.yaml infra job, the cd.yaml drift-gate job `with:`, and the
# composite action. A var added to infra but not wired through downstream
# would otherwise show as false-positive drift and block every deploy.
check-drift-gate-parity:
    bash scripts/check-drift-gate-parity.sh

# Run OpenTofu native tests (validation block contract per ADR 0024).
# Drop the cached terraform.tfstate first because a previous
# `tofu init` against the real GCS backend leaves a state pointer
# behind, which `tofu test` then tries to load — and fails with 403
# in CI / on developer machines without GCS read perms. Removing the
# cache forces a clean -backend=false init.
test-iac:
    cd tofu && rm -f .terraform/terraform.tfstate && tofu init -backend=false >/dev/null && tofu test

# Run project semgrep rules.
#
# Local `just semgrep` only fails on ERROR-severity findings. WARNING
# findings (e.g. release-gate auth_boundary content signatures) are
# escalation hints for the release-gate CI workflow, not local
# blockers — release-gate.yaml runs with both severities and uses
# the WARNING count to escalate the change category, while keeping
# ERROR as the only hard fail. Mirroring that policy locally lets
# developers iterate without false-positive blocks from the
# escalation rules. See ADR 0033 §"Two-tier Semgrep policy".
semgrep:
    semgrep --config .semgrep/rules/ --severity ERROR --error

lint-md:
    @{{MARKDOWNLINT}} --fix "*.md" "docs/**/*.md"

# Format code
fmt:
    gofmt -w .

# Verify code is gofmt-clean WITHOUT modifying files. Used as pre-commit /
# pre-PR gate so manual struct alignment / spacing drift fails locally
# before reaching CI's golangci-lint stage. Mirrors the canonical hash
# bump policy from `tap/refs/scripts/check_substrate_drift.sh`: gofmt
# 1.26.2's output is the canonical form, and any deviation is rejected.
fmt-check:
    @{{ if `gofmt -l . | head -1` == "" { "echo 'gofmt: clean'" } else { "echo 'gofmt: drift detected:' && gofmt -l . && exit 1" } }}

# Build Docker image
docker-build:
    docker build -t runops-gateway:local .

# Tidy dependencies
tidy:
    go mod tidy

# Run scenario tests. Requires the dev server running first:
#   SLACK_SIGNING_SECRET=test-secret just run
# --scopes run:exec lets each runbook shell out (openssl) to compute a fresh
# Slack signature for the current time, so the signed scenarios satisfy ADR
# 0016's ±5min timestamp freshness window instead of using stale hardcoded sigs.
test-runn:
    runn run --scopes run:exec tests/runn/*.yaml

# Run integration tests. testcontainers starts the firebase emulator inside the
# test process (ADR 0041) — no external emulator, no docker compose, no init
# scripts; only a running Docker daemon is required. Covers the Pub/Sub bridge
# (tests/integration) and the Firestore registry (internal/.../state).
test-integration:
    go test -tags=integration ./tests/integration/... ./internal/adapter/output/state/...

# Local OpenTelemetry tracing is provided by the shared dotfiles `tel` stack
# (otel-collector on :4317 -> Tempo/Grafana), not a per-repo Jaeger. There is no
# compose.yaml and no trace-up/down/view here anymore (ADR 0042). From a dotfiles
# checkout run `just tel-up`, then start binaries with
# OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317. ADR 0020 (direct OTLP
# export) is unchanged — only the local backend moved to the shared stack.

# Test notify-slack.sh: dry-run payload structure + bash/Go compress_gz round-trip
# Requires: bash, gzip, base64, jq
test-scripts:
    go test ./internal/adapter/input/slack/... -run TestNotifyScript -v

# Run all checks (used before commit). fmt-check (read-only) replaces
# the prior `fmt` (write) so a stale alignment fails the gate locally
# instead of silently rewriting and slipping into CI's golangci-lint.
# To auto-fix locally: `just fmt && just check`.
check: fmt-check lint lint-md test

# ------------------------------
# prek (j178/prek) — Rust reimplementation of pre-commit
# Install:   just install-hooks  (== prek install)
# Run all:   just pre-commit     (== prek run --all-files)
# Push gate: just check-all      (== prek + check + test)
# ------------------------------

# Install prek-managed git hooks once per clone
install-hooks:
    prek install

# Run every prek hook against all files
pre-commit:
    prek run --all-files

# CI-equivalent gate: prek hooks + check + full test suite
check-all: pre-commit check test
    @echo "✅ all checks passed"

# Local parity with .github/workflows/ci.yaml: vet + golangci-lint (lint),
# race-detector tests, build, tofu test (lint -> test-iac), and integration
# (testcontainers; needs a running Docker daemon). lint is a superset of the
# CI test job's lint steps — it adds semgrep + drift-gate parity, which CI
# runs in release-gate.yaml / cd.yaml instead.
ci: lint test-race build test-integration
    @echo "✅ ci parity gate passed"

# Copy initial setup files to a managed app repository.
# Usage: just init-app ../my-app my-project my-service,my-other-service my-migrate-job [asia-northeast1] [my-artifact-repo] [gateway-project]
init-app target app_project service_names migration_job region="asia-northeast1" artifact_repo="" gateway_project="":
    {{justfile_directory()}}/scripts/init-app.sh "{{target}}" "{{app_project}}" "{{service_names}}" "{{migration_job}}" "{{region}}" "{{artifact_repo}}" "{{gateway_project}}"

# Verify IAM, AR, secrets, and Cloud Run configuration for a managed app.
# Usage: just check-app my-project my-service,my-other-service my-migrate-job [asia-northeast1] [my-artifact-repo] [gateway-project]
check-app app_project service_names migration_job region="asia-northeast1" artifact_repo="" gateway_project="":
    {{justfile_directory()}}/scripts/check-app.sh "{{app_project}}" "{{service_names}}" "{{migration_job}}" "{{region}}" "{{artifact_repo}}" "{{gateway_project}}"
