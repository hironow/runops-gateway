#!/usr/bin/env bash
# notify-slack.sh — Build and POST the Slack deploy-approval Block Kit message.
#
# Usage:
#   notify-slack.sh [--dry-run] SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS
#
# Arguments:
#   SERVICE_NAMES       Comma-separated Cloud Run service names
#   MIGRATION_JOB_NAME  Cloud Run Job name for DB migration
#   BRANCH_NAME         Git branch name
#   COMMIT_SHA          Full Git commit SHA (at least 7 chars)
#   REVISIONS           Comma-separated revision names (same order as SERVICE_NAMES)
#
# Environment:
#   SLACK_WEBHOOK_URL   Slack Incoming Webhook URL (required unless --dry-run)
#
# Flags:
#   --dry-run           Print the JSON payload to stdout instead of sending to Slack.
#                       Used in tests to validate payload structure and button values.

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
  shift
fi

if [[ $# -lt 5 ]]; then
  echo "Usage: $0 [--dry-run] SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS" >&2
  exit 1
fi

SERVICE_NAMES="$1"
MIGRATION_JOB_NAME="$2"
BRANCH_NAME="$3"
COMMIT_SHA="$4"
REVISIONS="$5"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# compress_gz compresses a JSON string to the "gz:<base64url>" format expected
# by parseActionValue in adapter/input/slack/handler.go.
# Must stay in sync with compressButtonValue in adapter/output/slack/blockkit.go.
#
# Pipeline:
#   gzip -c          → RFC 1952 gzip stream to stdout
#   base64 -w 0      → standard base64, no line wrapping (Linux/Debian)
#   tr '+/' '-_'     → base64 → base64url (URL-safe alphabet)
#   tr -d '='        → strip padding (matches base64.RawURLEncoding in Go)
compress_gz() {
  printf '%s' "$1" | gzip -c | base64 -w 0 | tr '+/' '-_' | tr -d '='
}

# ---------------------------------------------------------------------------
# Build action values
# ---------------------------------------------------------------------------

TIMESTAMP=$(date +%s)
BUILD_INFO="${BRANCH_NAME} @ ${COMMIT_SHA:0:7}"

# Slack header block text is limited to 150 chars.
# "Deploy Ready: " is 14 chars, leaving 135 for service names.
SVC_DISPLAY="${SERVICE_NAMES}"
if [[ "${#SVC_DISPLAY}" -gt 135 ]]; then
  SVC_DISPLAY="${SVC_DISPLAY:0:134}…"
fi

# "1. DB Migration → Canary" button: run the migration job then offer canary step.
JOB_ACTION=$(jq -n \
  --arg rt   "job" \
  --arg rn   "${MIGRATION_JOB_NAME}" \
  --arg t    "" \
  --arg a    "migrate_apply" \
  --argjson ia "${TIMESTAMP}" \
  --arg nsn  "${SERVICE_NAMES}" \
  --arg nr   "${REVISIONS}" \
  --arg na   "canary_10" \
  '{resource_type:$rt, resource_names:$rn, targets:$t, action:$a,
    issued_at:$ia, migration_done:false,
    next_service_names:$nsn, next_revisions:$nr, next_action:$na}')

# "2. Canary (skip migration)" button: go straight to canary without DB migration.
SRV_ACTION=$(jq -n \
  --arg rt  "service" \
  --arg rn  "${SERVICE_NAMES}" \
  --arg t   "${REVISIONS}" \
  --arg a   "canary_10" \
  --argjson ia "${TIMESTAMP}" \
  '{resource_type:$rt, resource_names:$rn, targets:$t, action:$a,
    issued_at:$ia, migration_done:true}')

# "Deny" button: reject the deployment without performing any action.
DENY_ACTION=$(jq -n \
  --arg rt  "service" \
  --arg rn  "${SERVICE_NAMES}" \
  --arg t   "${REVISIONS}" \
  --arg a   "canary_10" \
  --argjson ia "${TIMESTAMP}" \
  '{resource_type:$rt, resource_names:$rn, targets:$t, action:$a, issued_at:$ia}')

# Compress all button values — matches marshalActionValue which always compresses
# so that the decompression path is exercised on every button click.
JOB_VALUE="gz:$(compress_gz "$JOB_ACTION")"
SRV_VALUE="gz:$(compress_gz "$SRV_ACTION")"
DENY_VALUE="gz:$(compress_gz "$DENY_ACTION")"

# ---------------------------------------------------------------------------
# Build Block Kit payload
# ---------------------------------------------------------------------------

PAYLOAD=$(jq -n \
  --arg svc        "${SVC_DISPLAY}" \
  --arg build_info "${BUILD_INFO}" \
  --arg revisions  "${REVISIONS}" \
  --arg job_val    "$JOB_VALUE" \
  --arg srv_val    "$SRV_VALUE" \
  --arg deny_val   "$DENY_VALUE" \
  '{
    blocks: [
      {
        type: "header",
        text: {type: "plain_text", text: ("Deploy Ready: " + $svc), emoji: true}
      },
      {
        type: "section",
        text: {
          type: "mrkdwn",
          text: ("*Revision(s):* `" + $revisions + "`\n*Build:* " + $build_info)
        },
        accessory: {
          type: "image",
          image_url: "https://placehold.co/75x75/FF0000/FFFFFF?text=PROD",
          alt_text: "PROD environment"
        }
      },
      {
        type: "actions",
        elements: [
          {
            type: "button",
            text: {type: "plain_text", emoji: true, text: "1. DB Migration \u2192 Canary"},
            style: "danger",
            action_id: "approve",
            value: $job_val
          },
          {
            type: "button",
            text: {type: "plain_text", emoji: true, text: "2. Canary (skip migration)"},
            style: "primary",
            action_id: "approve",
            value: $srv_val,
            confirm: {
              title: {type: "plain_text", text: "\u7d9a\u884c\u3057\u307e\u3059\u304b\uff1f"},
              text: {type: "mrkdwn", text: "DB\u30de\u30a4\u30b0\u30ec\u30fc\u30b7\u30e7\u30f3\u3092\u5b9f\u65bd\u3057\u307e\u3057\u305f\u304b\uff1f\u672a\u5b9f\u65bd\u306e\u5834\u5408\u306f\u5148\u306b\u5b9f\u884c\u3057\u3066\u304f\u3060\u3055\u3044\u3002"},
              confirm: {type: "plain_text", text: "\u306f\u3044\u3001\u7d9a\u884c\u3057\u307e\u3059"},
              deny: {type: "plain_text", text: "\u30ad\u30e3\u30f3\u30bb\u30eb"}
            }
          },
          {
            type: "button",
            text: {type: "plain_text", emoji: true, text: "\ud83d\udecf Deny"},
            style: "danger",
            action_id: "deny",
            value: $deny_val
          }
        ]
      }
    ]
  }')

# ---------------------------------------------------------------------------
# Send or dry-run
# ---------------------------------------------------------------------------

if [[ "$DRY_RUN" == "true" ]]; then
  printf '%s\n' "$PAYLOAD"
  exit 0
fi

if [[ -z "${SLACK_WEBHOOK_URL:-}" ]]; then
  echo "Error: SLACK_WEBHOOK_URL is not set" >&2
  exit 1
fi

curl -s -X POST \
  -H "Content-Type: application/json" \
  --data "${PAYLOAD}" \
  "${SLACK_WEBHOOK_URL}"
