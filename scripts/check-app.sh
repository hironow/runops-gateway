#!/usr/bin/env bash
# check-app.sh — Verify that a managed app's IAM, AR, secrets, and Cloud Run
# configuration are correctly set up for runops-gateway.
#
# Usage:
#   check-app.sh APP_PROJECT SERVICE_NAMES MIGRATION_JOB [REGION] [ARTIFACT_REPO] [GATEWAY_PROJECT]
#
# Arguments match init-app.sh (minus TARGET):
#   APP_PROJECT      GCP project ID of the managed app
#   SERVICE_NAMES    Comma-separated Cloud Run service names
#   MIGRATION_JOB    Cloud Run Job name for DB migration
#   REGION           GCP region (default: asia-northeast1)
#   ARTIFACT_REPO    Artifact Registry repository name (default: first service name)
#   GATEWAY_PROJECT  GCP project ID where runops-gateway is hosted (default: same as APP_PROJECT)

set -euo pipefail

APP_PROJECT="${1:?Error: APP_PROJECT is required}"
SERVICE_NAMES="${2:?Error: SERVICE_NAMES is required}"
MIGRATION_JOB="${3:?Error: MIGRATION_JOB is required}"
REGION="${4:-asia-northeast1}"
ARTIFACT_REPO="${5:-}"
GATEWAY_PROJECT="${6:-$APP_PROJECT}"

# Resolve defaults
if [[ -z "$ARTIFACT_REPO" ]]; then
  ARTIFACT_REPO="${SERVICE_NAMES%%,*}"
fi

CHATOPS_SA="slack-chatops-sa@${GATEWAY_PROJECT}.iam.gserviceaccount.com"
APP_PROJECT_NUMBER=$(gcloud projects describe "$APP_PROJECT" --format="value(projectNumber)" 2>/dev/null) || true
CLOUDBUILD_SA="${APP_PROJECT_NUMBER}@cloudbuild.gserviceaccount.com"
COMPUTE_SA="${APP_PROJECT_NUMBER}-compute@developer.gserviceaccount.com"

PASS=0
FAIL=0
WARN=0

check() {
  local label="$1"
  shift
  if "$@" > /dev/null 2>&1; then
    echo "  [OK] $label"
    PASS=$((PASS + 1))
  else
    echo "  [NG] $label"
    FAIL=$((FAIL + 1))
  fi
}

warn() {
  local label="$1"
  shift
  if "$@" > /dev/null 2>&1; then
    echo "  [OK] $label"
    PASS=$((PASS + 1))
  else
    echo "  [--] $label (optional)"
    WARN=$((WARN + 1))
  fi
}

echo "============================================================"
echo "  check-app"
echo "  app:       ${SERVICE_NAMES} (${APP_PROJECT})"
echo "  gateway:   ${GATEWAY_PROJECT}"
echo "  region:    ${REGION}"
echo "  ar_repo:   ${ARTIFACT_REPO}"
echo "  job:       ${MIGRATION_JOB}"
echo "============================================================"
echo ""

# ---- 1. Cloud Run Services exist ----
echo "[Cloud Run Service]"
IFS=',' read -ra SERVICES <<< "$SERVICE_NAMES"
for SVC in "${SERVICES[@]}"; do
  SVC=$(echo "$SVC" | xargs)
  check "Service '${SVC}' exists" \
    gcloud run services describe "$SVC" \
      --project="$APP_PROJECT" --region="$REGION" --format="value(name)"
done

# ---- 2. chatops SA → run.developer (project-level) ----
echo ""
echo "[IAM: chatops SA -> run.developer (project-level)]"
check "run.developer on project ${APP_PROJECT}" \
  bash -c "gcloud projects get-iam-policy '$APP_PROJECT' \
    --flatten='bindings[].members' \
    --filter='bindings.members:$CHATOPS_SA AND bindings.role:roles/run.developer' \
    --format='value(bindings.role)' 2>/dev/null | grep -q 'run.developer'"

# ---- 3. chatops SA → iam.serviceAccountUser on runtime SA ----
echo ""
echo "[IAM: chatops SA -> Runtime SA]"
SA_IAM=$(gcloud iam service-accounts get-iam-policy "$COMPUTE_SA" \
  --project="$APP_PROJECT" --format=json 2>/dev/null || echo "{}")
check "iam.serviceAccountUser on ${COMPUTE_SA}" \
  bash -c "echo '$SA_IAM' | grep -q 'iam.serviceAccountUser' && echo '$SA_IAM' | grep -q '$CHATOPS_SA'"

# ---- 4. chatops SA → artifactregistry.reader ----
echo ""
echo "[IAM: chatops SA -> Artifact Registry]"
AR_IAM=$(gcloud artifacts repositories get-iam-policy "$ARTIFACT_REPO" \
  --project="$APP_PROJECT" --location="$REGION" --format=json 2>/dev/null || echo "{}")
check "artifactregistry.reader on ${ARTIFACT_REPO}" \
  bash -c "echo '$AR_IAM' | grep -q 'artifactregistry.reader' && echo '$AR_IAM' | grep -q '$CHATOPS_SA'"

# ---- 5. Cloud Build SA → secret accessor on slack-webhook-url ----
echo ""
echo "[IAM: Cloud Build SA -> Secret Manager (${GATEWAY_PROJECT})]"
SECRET_IAM=$(gcloud secrets get-iam-policy slack-webhook-url \
  --project="$GATEWAY_PROJECT" --format=json 2>/dev/null || echo "{}")
check "secretAccessor on slack-webhook-url" \
  bash -c "echo '$SECRET_IAM' | grep -q 'secretmanager.secretAccessor' && echo '$SECRET_IAM' | grep -q '$CLOUDBUILD_SA'"

# ---- 6. Artifact Registry repo exists ----
echo ""
echo "[Artifact Registry]"
check "Repository '${ARTIFACT_REPO}' exists" \
  gcloud artifacts repositories describe "$ARTIFACT_REPO" \
    --project="$APP_PROJECT" --location="$REGION" --format="value(name)"

# ---- 7. Cloud Run Job (optional) ----
echo ""
echo "[Cloud Run Job]"
warn "Job '${MIGRATION_JOB}' exists" \
  gcloud run jobs describe "$MIGRATION_JOB" \
    --project="$APP_PROJECT" --region="$REGION" --format="value(name)"

# run.developer はプロジェクトレベルで確認済み（セクション 2）のため、ジョブ単位のチェックは不要

# ---- 8. WIF ----
echo ""
echo "[Workload Identity Federation]"
warn "WIF pool 'github' exists in ${APP_PROJECT}" \
  gcloud iam workload-identity-pools describe github \
    --project="$APP_PROJECT" --location=global --format="value(name)"

# ---- Summary ----
echo ""
echo "============================================================"
echo "  Results: ${PASS} passed, ${FAIL} failed, ${WARN} skipped"
echo "============================================================"

if [[ $FAIL -gt 0 ]]; then
  echo ""
  echo "  Fix [NG] items before runops-gateway can operate on this app."
  if [[ "$GATEWAY_PROJECT" == "$APP_PROJECT" ]]; then
    echo "  See docs/guide-single-project.md"
  else
    echo "  See docs/guide-two-projects.md"
  fi
  exit 1
fi
