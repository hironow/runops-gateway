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

# Probe the emulator REST API with bounded retries before issuing
# any PUT. The docker compose container's health check passes once
# the SUPERVISOR is up; the actual Pub/Sub REST listener on 9399
# can lag a few seconds behind. We rely on curl's exit code (not
# %{http_code}) so the per-attempt retry repetition bug from the
# earlier rewrite cannot reappear.
#
#   exit 0  → server returned a response (any status; success-shaped)
#   exit 7  → connect refused (= REST listener not up)
#   exit 28 → timeout (= server slow / overloaded)
#
# Any non-zero exit is treated as not-ready and we retry up to 30
# times (60 sec total at delay=2). 30 was chosen empirically after
# observing emulator boots in the 8-15 sec range on ubuntu-24.04
# CI runners; doubling the upper bound gives margin for slow
# concurrent runs.
echo "  Probing emulator REST API readiness..."
ready=false
for i in $(seq 1 30); do
  if curl --silent --output /dev/null --max-time 2 "${PUBSUB_BASE}/projects/${PROJECT}/topics"; then
    echo "  Emulator REST API ready (attempt ${i})"
    ready=true
    break
  fi
  sleep 2
done
if [[ "${ready}" != "true" ]]; then
  echo "ERROR: emulator REST API at ${PUBSUB_BASE} did not respond after 60s" >&2
  exit 1
fi

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
  # Some curl versions emit %{http_code} once per retry; only the
  # final attempt's status matters, so collapse to the last 3 chars.
  http_code="${http_code: -3}"
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
  http_code="${http_code: -3}"
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
