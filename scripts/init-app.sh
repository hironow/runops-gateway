#!/usr/bin/env bash
# init-app.sh — Copy and configure CI/CD files for a managed app repository.
#
# Usage:
#   init-app.sh TARGET APP_PROJECT SERVICE_NAMES MIGRATION_JOB [REGION] [ARTIFACT_REPO] [GATEWAY_PROJECT]
#
# Arguments:
#   TARGET           Path to the managed app repository
#   APP_PROJECT      GCP project ID of the managed app
#   SERVICE_NAMES    Comma-separated Cloud Run service names
#   MIGRATION_JOB    Cloud Run Job name for DB migration
#   REGION           GCP region (default: asia-northeast1)
#   ARTIFACT_REPO    Artifact Registry repository name (default: first service name)
#   GATEWAY_PROJECT  GCP project ID where runops-gateway is hosted (default: same as APP_PROJECT)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="$(dirname "$SCRIPT_DIR")"

TARGET="${1:?Error: TARGET directory is required}"
APP_PROJECT="${2:?Error: APP_PROJECT is required}"
SERVICE_NAMES="${3:?Error: SERVICE_NAMES is required}"
MIGRATION_JOB="${4:?Error: MIGRATION_JOB is required}"
REGION="${5:-asia-northeast1}"
ARTIFACT_REPO="${6:-}"
GATEWAY_PROJECT="${7:-}"

if [[ ! -d "$TARGET" ]]; then
  echo "Error: target directory does not exist: $TARGET" >&2
  exit 1
fi

# Resolve artifact repo name: explicit arg > first service name
if [[ -z "$ARTIFACT_REPO" ]]; then
  ARTIFACT_REPO="${SERVICE_NAMES%%,*}"
fi

# --- scripts/notify-slack.sh (copy as-is) ---
mkdir -p "$TARGET/scripts"
cp "$SRC_DIR/scripts/notify-slack.sh" "$TARGET/scripts/notify-slack.sh"
chmod +x "$TARGET/scripts/notify-slack.sh"
echo "  copied scripts/notify-slack.sh"

# --- cloudbuild.yaml (copy with substitutions) ---
content=$(<"$SRC_DIR/cloudbuild.yaml")
# Gateway project substitution must happen BEFORE ${PROJECT_ID} replacement
if [[ -n "$GATEWAY_PROJECT" ]]; then
  content="${content//_GATEWAY_PROJECT: \$\{PROJECT_ID\}/_GATEWAY_PROJECT: ${GATEWAY_PROJECT}}"
fi
content="${content//\$\{PROJECT_ID\}/${APP_PROJECT}}"
content="${content//runops\/runops-gateway/${ARTIFACT_REPO}/${ARTIFACT_REPO}}"
content="${content//_SERVICE_NAMES: frontend-service/_SERVICE_NAMES: ${SERVICE_NAMES}}"
content="${content//_MIGRATION_JOB_NAME: db-migrate-job/_MIGRATION_JOB_NAME: ${MIGRATION_JOB}}"
content="${content//_REGION: asia-northeast1/_REGION: ${REGION}}"
printf '%s\n' "$content" > "$TARGET/cloudbuild.yaml"
echo "  copied cloudbuild.yaml"

echo ""
echo "Done. Review the generated files:"
echo "  $TARGET/cloudbuild.yaml"
echo "  $TARGET/scripts/notify-slack.sh"
