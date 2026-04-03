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

# Run linting
lint:
    go vet ./...

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

# Test notify-slack.sh: dry-run payload structure + bash/Go compress_gz round-trip
# Requires: bash, gzip, base64, jq
test-scripts:
    go test ./internal/adapter/input/slack/... -run TestNotifyScript -v

# Run all checks (used before commit)
check: fmt lint lint-md test

# Copy initial setup files to a managed app repository.
# Usage: just init-app ../my-app my-service,my-other-service my-migrate-job [asia-northeast1] [my-artifact-repo]
init-app target service_names migration_job region="asia-northeast1" artifact_repo="":
    #!/usr/bin/env bash
    set -euo pipefail

    target="{{target}}"
    service_names="{{service_names}}"
    migration_job="{{migration_job}}"
    region="{{region}}"
    artifact_repo="{{artifact_repo}}"
    src="{{justfile_directory()}}"

    if [[ ! -d "$target" ]]; then
      echo "Error: target directory does not exist: $target" >&2
      exit 1
    fi

    # Resolve artifact repo name: explicit arg > first service name
    if [[ -z "$artifact_repo" ]]; then
      artifact_repo="${service_names%%,*}"
    fi

    # --- scripts/notify-slack.sh (copy as-is) ---
    mkdir -p "$target/scripts"
    cp "$src/scripts/notify-slack.sh" "$target/scripts/notify-slack.sh"
    chmod +x "$target/scripts/notify-slack.sh"
    echo "  copied scripts/notify-slack.sh"

    # --- cloudbuild.yaml (copy with substitutions) ---
    sed \
      -e "s|runops/runops-gateway|${artifact_repo}/${artifact_repo}|g" \
      -e "s|_SERVICE_NAMES: frontend-service|_SERVICE_NAMES: ${service_names}|g" \
      -e "s|_MIGRATION_JOB_NAME: db-migrate-job|_MIGRATION_JOB_NAME: ${migration_job}|g" \
      -e "s|_REGION: asia-northeast1|_REGION: ${region}|g" \
      "$src/cloudbuild.yaml" > "$target/cloudbuild.yaml"
    echo "  copied cloudbuild.yaml"

    echo ""
    echo "Done. Review the generated files:"
    echo "  $target/cloudbuild.yaml"
    echo "  $target/scripts/notify-slack.sh"
