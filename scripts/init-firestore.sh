#!/usr/bin/env bash
# Probe the Firestore emulator (compose service "pubsub-emulator", which
# bundles the Firestore emulator on port 8080) and confirm it answers a
# document write/delete round-trip. The script is intentionally side-effect
# free at the application level: it writes to a sentinel collection
# (`_init/sentinel`) and immediately deletes the document so the registry
# `projects` collection stays untouched.
#
# Mirrors scripts/init-pubsub.sh in spirit. Idempotent — safe to re-run.
#
# Usage:
#   just firestore-init
# or:
#   FIRESTORE_HOST=localhost:8080 GOOGLE_CLOUD_PROJECT=runops-local ./scripts/init-firestore.sh
set -euo pipefail

FIRESTORE_HOST="${FIRESTORE_HOST:-localhost:8080}"
PROJECT="${GOOGLE_CLOUD_PROJECT:-runops-local}"
DATABASE="${RUNOPS_FIRESTORE_DATABASE:-(default)}"
BASE="http://${FIRESTORE_HOST}/v1/projects/${PROJECT}/databases/${DATABASE}/documents"

echo "Probing Firestore emulator at ${BASE} ..."

write_payload='{"fields":{"ok":{"booleanValue":true}}}'

http_status=$(curl -sf -o /tmp/init-firestore.body -w '%{http_code}' \
  -X PATCH "${BASE}/_init/sentinel" \
  -H "Content-Type: application/json" \
  -d "${write_payload}" || true)

case "${http_status}" in
  200|201)
    echo "  Sentinel write OK (HTTP ${http_status})"
    ;;
  *)
    echo "Firestore emulator did not accept sentinel write (HTTP ${http_status:-unknown})." >&2
    echo "  Body: $(cat /tmp/init-firestore.body 2>/dev/null || echo '<empty>')" >&2
    echo "  Hint: run 'just firestore-up' first." >&2
    exit 1
    ;;
esac

curl -sf -X DELETE "${BASE}/_init/sentinel" >/dev/null 2>&1 || true
echo "Firestore emulator ready (project=${PROJECT}, database=${DATABASE})."
