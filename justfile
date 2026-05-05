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

# Run linting (go vet + golangci-lint)
lint:
    go vet ./...
    CGO_ENABLED=0 go tool -modfile=tools/go.mod golangci-lint run ./...

lint-md:
    @{{MARKDOWNLINT}} --fix "*.md" "docs/**/*.md"

# Format code
fmt:
    gofmt -w .

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

# Run all checks (used before commit)
check: fmt lint lint-md test

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
