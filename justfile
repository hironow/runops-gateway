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

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-v:
    go test -v ./...

# Run linting (go vet + golangci-lint + semgrep + tofu test)
lint:
    go vet ./...
    CGO_ENABLED=0 go tool -modfile=tools/go.mod golangci-lint run ./...
    just semgrep
    just test-iac

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

# Run scenario tests (requires server to be running with SLACK_SIGNING_SECRET=test-secret)
test-runn:
    runn run tests/runn/*.yaml

# Run integration tests (require Pub/Sub emulator: just pubsub-up + just pubsub-init)
test-integration:
    PUBSUB_EMULATOR_HOST=localhost:9399 PUBSUB_PROJECT_ID=runops-local \
        go test -tags=integration ./tests/integration/...

# Start Pub/Sub emulator (Phase 2a/2b/2c local development)
pubsub-up:
    docker compose -f compose.yaml up -d pubsub-emulator
    @echo "waiting for emulator to be healthy (timeout 60s)..."
    @timeout 60 sh -c 'until docker inspect -f "{{{{.State.Health.Status}}}}" runops-pubsub-emulator | grep -q healthy; do sleep 2; done'
    @echo "emulator ready: http://localhost:9399 (UI: http://localhost:4000)"

# Initialize topics + subscriptions on the running emulator
pubsub-init:
    {{justfile_directory()}}/scripts/init-pubsub.sh

# Stop the emulator
pubsub-down:
    docker compose -f compose.yaml stop pubsub-emulator

# Start Firestore emulator (issue #0011 — bundled in the same firebase
# image as Pub/Sub, so this is an alias for pubsub-up that emphasizes the
# Firestore use case)
firestore-up: pubsub-up
    @echo "firestore emulator ready: http://localhost:8080 (UI: http://localhost:4000)"

# Probe the Firestore emulator with a sentinel doc round-trip
firestore-init:
    {{justfile_directory()}}/scripts/init-firestore.sh

# Stop the Firestore emulator (alias for pubsub-down — same container)
firestore-down: pubsub-down

# Run Firestore integration tests against the emulator
test-firestore-integration:
    FIRESTORE_EMULATOR_HOST=localhost:8080 GOOGLE_CLOUD_PROJECT=runops-local \
        go test -tags=integration -run Firestore ./internal/adapter/output/state/...

# Start local Jaeger v2 (OpenTelemetry trace backend, ADR 0020)
trace-up:
    docker compose -f compose.yaml up -d jaeger
    @echo "Jaeger UI: http://localhost:16686"
    @echo "Set OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 in your shell to ship spans there."

# Stop local Jaeger
trace-down:
    docker compose -f compose.yaml stop jaeger

# Open Jaeger UI in browser
trace-view:
    @command -v open >/dev/null 2>&1 && open http://localhost:16686 \
        || command -v xdg-open >/dev/null 2>&1 && xdg-open http://localhost:16686 \
        || echo "Please open http://localhost:16686 manually"

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

# Copy initial setup files to a managed app repository.
# Usage: just init-app ../my-app my-project my-service,my-other-service my-migrate-job [asia-northeast1] [my-artifact-repo] [gateway-project]
init-app target app_project service_names migration_job region="asia-northeast1" artifact_repo="" gateway_project="":
    {{justfile_directory()}}/scripts/init-app.sh "{{target}}" "{{app_project}}" "{{service_names}}" "{{migration_job}}" "{{region}}" "{{artifact_repo}}" "{{gateway_project}}"

# Verify IAM, AR, secrets, and Cloud Run configuration for a managed app.
# Usage: just check-app my-project my-service,my-other-service my-migrate-job [asia-northeast1] [my-artifact-repo] [gateway-project]
check-app app_project service_names migration_job region="asia-northeast1" artifact_repo="" gateway_project="":
    {{justfile_directory()}}/scripts/check-app.sh "{{app_project}}" "{{service_names}}" "{{migration_job}}" "{{region}}" "{{artifact_repo}}" "{{gateway_project}}"
