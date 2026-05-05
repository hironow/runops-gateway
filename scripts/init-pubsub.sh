#!/usr/bin/env bash
# Idempotently create the Pub/Sub topics + subscriptions runops-gateway uses.
# Targets the local Firebase emulator (compose.yaml service "pubsub-emulator").
#
# Defaults match compose.yaml + cmd/server's PUBSUB_PROJECT_ID convention; can
# be overridden with env vars before running.
#
# Usage:
#   just pubsub-init
# or:
#   PUBSUB_HOST=localhost:9399 PUBSUB_PROJECT_ID=runops-local ./scripts/init-pubsub.sh
set -euo pipefail

PUBSUB_HOST="${PUBSUB_HOST:-localhost:9399}"
PROJECT="${PUBSUB_PROJECT_ID:-runops-local}"
PUBSUB_BASE="http://${PUBSUB_HOST}/v1"

echo "Initializing Pub/Sub topics and subscriptions on ${PUBSUB_BASE} (project=${PROJECT})..."

create_topic() {
  local name="$1"
  curl -sf -X PUT "${PUBSUB_BASE}/projects/${PROJECT}/topics/${name}" >/dev/null 2>&1 || true
  echo "  Topic: ${name}"
}

create_pull_sub() {
  local name="$1"
  local topic="$2"
  local ack_deadline="${3:-60}"
  curl -sf -X PUT "${PUBSUB_BASE}/projects/${PROJECT}/subscriptions/${name}" \
    -H "Content-Type: application/json" \
    -d "{\"topic\":\"projects/${PROJECT}/topics/${topic}\",\"ackDeadlineSeconds\":${ack_deadline}}" \
    >/dev/null 2>&1 || true
  echo "  Subscription (pull): ${name} -> ${topic}"
}

# Phase 2a: gateway -> dmail-receiver path
create_topic "dmail-inbound"
create_topic "dmail-inbound-dlq"
create_pull_sub "dmail-receiver-sub" "dmail-inbound" 60

# Phase 2c: dmail-emitter -> gateway path
create_topic "dmail-outbound"
create_topic "dmail-outbound-dlq"
create_pull_sub "runops-gateway-sub" "dmail-outbound" 60

echo "Pub/Sub initialization complete."
