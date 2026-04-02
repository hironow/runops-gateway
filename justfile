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
