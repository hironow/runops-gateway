#!/usr/bin/env bash
# notify-slack.sh — Build and POST the Slack deploy-approval Block Kit message.
#
# Usage:
#   notify-slack.sh [--dry-run] SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS PROJECT_ID REGION [WORKER_POOL_NAMES WORKER_POOL_REVISIONS]
#
# Arguments:
#   SERVICE_NAMES           Comma-separated Cloud Run service names
#   MIGRATION_JOB_NAME      Cloud Run Job name for DB migration
#   BRANCH_NAME             Git branch name
#   COMMIT_SHA              Full Git commit SHA (at least 7 chars)
#   REVISIONS               Comma-separated revision names (same order as SERVICE_NAMES)
#   PROJECT_ID              GCP project ID where the managed app runs
#   REGION                  Cloud Run region (e.g. asia-northeast1)
#   WORKER_POOL_NAMES       (optional) Comma-separated Cloud Run worker pool names
#   WORKER_POOL_REVISIONS   (optional) Comma-separated worker pool revisions
#                           (same order as WORKER_POOL_NAMES)
#
# Environment:
#   SLACK_WEBHOOK_URL       Slack Incoming Webhook URL (required unless --dry-run)
#
# Flags:
#   --dry-run               Print the JSON payload to stdout instead of sending to Slack.
#                           Used in tests to validate payload structure and button values.

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

DRY_RUN=false
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=true
  shift
fi

if [[ $# -lt 7 ]]; then
  echo "Usage: $0 [--dry-run] SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS PROJECT_ID REGION [WORKER_POOL_NAMES WORKER_POOL_REVISIONS]" >&2
  exit 1
fi

SERVICE_NAMES="$1"
MIGRATION_JOB_NAME="$2"
BRANCH_NAME="$3"
COMMIT_SHA="$4"
REVISIONS="$5"
PROJECT_ID="$6"
REGION="$7"
WORKER_POOL_NAMES="${8:-}"
WORKER_POOL_REVISIONS="${9:-}"

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
# Only emitted when MIGRATION_JOB_NAME is non-empty. Apps without a migration job
# (e.g. static sites, no Cloud SQL) leave this empty so the button is suppressed —
# pressing it would otherwise trigger a Cloud SQL backup against a non-existent
# instance and fail with a confusing 403/404.
JOB_ACTION=""
if [[ -n "${MIGRATION_JOB_NAME}" ]]; then
  JOB_ACTION=$(jq -n \
    --arg p    "${PROJECT_ID}" \
    --arg l    "${REGION}" \
    --arg rt   "job" \
    --arg rn   "${MIGRATION_JOB_NAME}" \
    --arg t    "" \
    --arg a    "migrate_apply" \
    --argjson ia "${TIMESTAMP}" \
    --arg nsn  "${SERVICE_NAMES}" \
    --arg nr   "${REVISIONS}" \
    --arg na   "canary_10" \
    --arg bi   "${BUILD_INFO}" \
    '{project:$p, location:$l, resource_type:$rt, resource_names:$rn, targets:$t, action:$a,
      issued_at:$ia, migration_done:false,
      next_service_names:$nsn, next_revisions:$nr, next_action:$na, build_info:$bi}')
fi

# "2. Canary (skip migration)" button: go straight to canary without DB migration.
SRV_ACTION=$(jq -n \
  --arg p   "${PROJECT_ID}" \
  --arg l   "${REGION}" \
  --arg rt  "service" \
  --arg rn  "${SERVICE_NAMES}" \
  --arg t   "${REVISIONS}" \
  --arg a   "canary_10" \
  --argjson ia "${TIMESTAMP}" \
  --arg bi  "${BUILD_INFO}" \
  '{project:$p, location:$l, resource_type:$rt, resource_names:$rn, targets:$t, action:$a,
    issued_at:$ia, migration_done:true, build_info:$bi}')

# "3. Worker Pool Canary" button: promote worker pools independently from services.
# resource_type=worker-pool dispatches to ApproveAction → approveShift → UpdateWorkerPool.
# Only emitted when WORKER_POOL_NAMES is non-empty (build may target pool-less envs).
WP_ACTION=""
if [[ -n "${WORKER_POOL_NAMES}" ]]; then
  WP_ACTION=$(jq -n \
    --arg p   "${PROJECT_ID}" \
    --arg l   "${REGION}" \
    --arg rt  "worker-pool" \
    --arg rn  "${WORKER_POOL_NAMES}" \
    --arg t   "${WORKER_POOL_REVISIONS}" \
    --arg a   "canary_10" \
    --argjson ia "${TIMESTAMP}" \
    --arg bi  "${BUILD_INFO}" \
    '{project:$p, location:$l, resource_type:$rt, resource_names:$rn, targets:$t, action:$a,
      issued_at:$ia, migration_done:true, build_info:$bi}')
fi

# "Deny" button: reject the deployment without performing any action.
DENY_ACTION=$(jq -n \
  --arg p   "${PROJECT_ID}" \
  --arg l   "${REGION}" \
  --arg rt  "service" \
  --arg rn  "${SERVICE_NAMES}" \
  --arg t   "${REVISIONS}" \
  --arg a   "canary_10" \
  --argjson ia "${TIMESTAMP}" \
  --arg bi  "${BUILD_INFO}" \
  '{project:$p, location:$l, resource_type:$rt, resource_names:$rn, targets:$t, action:$a, issued_at:$ia, build_info:$bi}')

# Compress all button values — matches marshalActionValue which always compresses
# so that the decompression path is exercised on every button click.
SRV_VALUE="gz:$(compress_gz "$SRV_ACTION")"
DENY_VALUE="gz:$(compress_gz "$DENY_ACTION")"
JOB_VALUE=""
if [[ -n "${JOB_ACTION}" ]]; then
  JOB_VALUE="gz:$(compress_gz "$JOB_ACTION")"
fi
WP_VALUE=""
if [[ -n "${WP_ACTION}" ]]; then
  WP_VALUE="gz:$(compress_gz "$WP_ACTION")"
fi

# ---------------------------------------------------------------------------
# Build Block Kit payload
# ---------------------------------------------------------------------------

PAYLOAD=$(jq -n \
  --arg svc        "${SVC_DISPLAY}" \
  --arg build_info "${BUILD_INFO}" \
  --arg revisions  "${REVISIONS}" \
  --arg pool_names "${WORKER_POOL_NAMES}" \
  --arg pool_revs  "${WORKER_POOL_REVISIONS}" \
  --arg job_val    "$JOB_VALUE" \
  --arg srv_val    "$SRV_VALUE" \
  --arg wp_val     "$WP_VALUE" \
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
          text: (":rotating_light: *PROD*\n*Revision(s):* `" + $revisions + "`\n*Build:* " + $build_info
            + (if $pool_names != "" then "\n*Worker Pool(s):* `" + $pool_names + "` @ `" + $pool_revs + "`" else "" end))
        }
      },
      {
        type: "actions",
        elements: (
          (if $job_val != "" then [{
              type: "button",
              text: {type: "plain_text", emoji: true, text: "1. DB Migration → Canary"},
              style: "danger",
              action_id: "approve_job",
              value: $job_val
            }] else [] end)
          + [
            {
              type: "button",
              text: {type: "plain_text", emoji: true, text: "2. Canary (skip migration)"},
              style: "primary",
              action_id: "approve_service",
              value: $srv_val,
              confirm: {
                title: {type: "plain_text", text: "続行しますか？"},
                text: {type: "mrkdwn", text: "DBマイグレーションを実施しましたか？未実施の場合は先に実行してください。"},
                confirm: {type: "plain_text", text: "はい、続行します"},
                deny: {type: "plain_text", text: "キャンセル"}
              }
            }
          ]
          + (if $wp_val != "" then [{
              type: "button",
              text: {type: "plain_text", emoji: true, text: "3. Worker Pool Canary"},
              style: "primary",
              action_id: "approve_worker_pool",
              value: $wp_val
            }] else [] end)
          + [{
              type: "button",
              text: {type: "plain_text", emoji: true, text: "🛏 Deny"},
              style: "danger",
              action_id: "deny",
              value: $deny_val
            }]
        )
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
