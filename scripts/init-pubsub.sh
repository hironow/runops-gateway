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
#
# Failure mode (history):
#
# Earlier versions of this script swallowed every curl failure with
# `|| true` + `2>/dev/null`, so a flaky emulator (= REST API not yet
# ready when the docker health check passes) could leave the topics
# uncreated and the integration tests would later die with
# "Topic not found". Observed across 5+ unrelated PRs in 2026-05-08.
#
# This version is fail-loud: each curl uses --fail-with-body +
# --retry 5 --retry-delay 2 --retry-connrefused so transient connect-
# refused / 5xx are absorbed but a true failure crashes the script
# AND the calling CI step. A pre-loop probe also waits for the
# emulator REST API to respond before issuing any PUT.
set -euo pipefail

PUBSUB_HOST="${PUBSUB_HOST:-localhost:9399}"
PROJECT="${PUBSUB_PROJECT_ID:-runops-local}"
PUBSUB_BASE="http://${PUBSUB_HOST}/v1"

echo "Initializing Pub/Sub topics and subscriptions on ${PUBSUB_BASE} (project=${PROJECT})..."

# Common curl flags: retry on transient failures; fail loudly on a
# real upstream error so the calling CI step surfaces it.
CURL_FLAGS=(
  --silent
  --show-error
  --retry 5
  --retry-delay 2
  --retry-connrefused
)

# Probe the emulator REST endpoint until it responds before issuing
# any PUT. The healthy docker compose container does NOT guarantee
# the REST API is up — they race. We GET the project listing as a
# smoke read; ANY 2xx / 4xx response means the server is alive.
# Connect refused / network errors trigger --retry-connrefused.
echo "  Waiting for emulator REST API to respond..."
probe_code=$(curl --silent --show-error --retry 10 --retry-delay 2 --retry-connrefused --max-time 5 \
  --output /dev/null \
  --write-out "%{http_code}" \
  "${PUBSUB_BASE}/projects/${PROJECT}/topics" || echo "000")
if [[ "${probe_code}" == "000" ]]; then
  echo "ERROR: emulator REST API at ${PUBSUB_BASE} unreachable" >&2
  exit 1
fi
echo "  Emulator REST API ready (probe http=${probe_code})"

create_topic() {
  local name="$1"
  # PUT is idempotent in the emulator: re-PUTting an existing topic
  # returns 409 ALREADY_EXISTS, which we explicitly tolerate. Other
  # 4xx / 5xx surface as a hard error.
  local http_code
  http_code=$(curl "${CURL_FLAGS[@]}" \
    --output /tmp/init-pubsub-resp.txt \
    --write-out "%{http_code}" \
    -X PUT "${PUBSUB_BASE}/projects/${PROJECT}/topics/${name}" || echo "000")
  case "${http_code}" in
    200|201)
      echo "  Topic created: ${name}"
      ;;
    409)
      echo "  Topic exists:  ${name}"
      ;;
    *)
      echo "ERROR: create topic ${name} failed (http=${http_code})" >&2
      cat /tmp/init-pubsub-resp.txt >&2 || true
      exit 1
      ;;
  esac
}

create_pull_sub() {
  local name="$1"
  local topic="$2"
  local ack_deadline="${3:-60}"
  local http_code
  http_code=$(curl "${CURL_FLAGS[@]}" \
    -H "Content-Type: application/json" \
    --output /tmp/init-pubsub-resp.txt \
    --write-out "%{http_code}" \
    -X PUT "${PUBSUB_BASE}/projects/${PROJECT}/subscriptions/${name}" \
    -d "{\"topic\":\"projects/${PROJECT}/topics/${topic}\",\"ackDeadlineSeconds\":${ack_deadline}}" || echo "000")
  case "${http_code}" in
    200|201)
      echo "  Subscription created: ${name} -> ${topic}"
      ;;
    409)
      echo "  Subscription exists:  ${name} -> ${topic}"
      ;;
    *)
      echo "ERROR: create sub ${name} -> ${topic} failed (http=${http_code})" >&2
      cat /tmp/init-pubsub-resp.txt >&2 || true
      exit 1
      ;;
  esac
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
