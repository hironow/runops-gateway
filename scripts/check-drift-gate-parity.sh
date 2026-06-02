#!/usr/bin/env bash
# Assert TF_VAR parity across the four layers that feed the tofu drift gate /
# radar (ADR 0043, ADR 0044). A var present in the infra Apply job but missing
# downstream falls back to the action default "" and shows as false-positive
# drift, which blocks every deploy (gate) or files a bogus issue daily (radar).
#
#   (a) .github/workflows/cd.yaml          infra job        TF_VAR_* env  (source of truth)
#   (b) .github/workflows/cd.yaml          drift-gate job   with: inputs  (mapped to TF_VAR names)
#   (c) .github/workflows/drift-detect.yaml radar job       with: inputs  (mapped to TF_VAR names)
#   (d) .github/actions/tofu-drift-gate/action.yaml         TF_VAR_* env
#
# wif_provider / service_account / fail_on_drift are action control inputs, not
# tofu vars, so they are dropped from the mapping. state_bucket maps to the
# non-obvious TF_VAR_tofu_state_bucket.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Paths default to the real files; tests override them with fixtures.
CD="${1:-$ROOT/.github/workflows/cd.yaml}"
ACTION="${2:-$ROOT/.github/actions/tofu-drift-gate/action.yaml}"
DETECT="${3:-$ROOT/.github/workflows/drift-detect.yaml}"

# Map a stream of with-input keys (stdin) to TF_VAR names.
tfvars_from_keys() {
  while IFS= read -r k; do
    [ -z "$k" ] && continue
    case "$k" in
      wif_provider | service_account | fail_on_drift) continue ;;
      state_bucket) echo "TF_VAR_tofu_state_bucket" ;;
      *) echo "TF_VAR_${k}" ;;
    esac
  done | sort -u
}

# (a) infra job: TF_VAR_* key lines only (prose mentions are not counted).
infra=$(awk '/^  infra:/{f=1} /^  drift-gate:/{f=0} f' "$CD" \
  | grep -E '^[[:space:]]+TF_VAR_[a-z0-9_]+:' | grep -oE 'TF_VAR_[a-z0-9_]+' | sort -u)

# (d) action: TF_VAR_* key lines only (input descriptions are not counted).
action=$(grep -E '^[[:space:]]+TF_VAR_[a-z0-9_]+:' "$ACTION" | grep -oE 'TF_VAR_[a-z0-9_]+' | sort -u)

# (b) cd.yaml drift-gate `with:` keys, bounded by the deploy job.
gate=$(awk '
  /^  drift-gate:/{f=1} /^  deploy:/{f=0; u=0}
  f && /uses: \.\/\.github\/actions\/tofu-drift-gate/{u=1; next}
  f && u && /^          [a-z0-9_]+:/{print}
' "$CD" | sed -E 's/^ *([a-z0-9_]+):.*/\1/' | tfvars_from_keys)

# (c) drift-detect.yaml radar `with:` keys, bounded by the next step (`      - `).
detect=$(awk '
  /uses: \.\/\.github\/actions\/tofu-drift-gate/{u=1; next}
  u && /^      - /{u=0}
  u && /^          [a-z0-9_]+:/{print}
' "$DETECT" | sed -E 's/^ *([a-z0-9_]+):.*/\1/' | tfvars_from_keys)

fail=0
report() {
  local label="$1" diff="$2"
  if [ -n "$diff" ]; then
    echo "MISMATCH ($label):" >&2
    echo "$diff" | sed 's/^/  /' >&2
    fail=1
  fi
}
report "infra TF_VAR vs cd.yaml drift-gate with:" "$(comm -3 <(echo "$infra") <(echo "$gate"))"
report "infra TF_VAR vs drift-detect.yaml radar with:" "$(comm -3 <(echo "$infra") <(echo "$detect"))"
report "infra TF_VAR vs action TF_VAR" "$(comm -3 <(echo "$infra") <(echo "$action"))"

if [ "$fail" -ne 0 ]; then
  echo "drift-gate input parity check FAILED — keep cd.yaml, drift-detect.yaml and action.yaml in lockstep." >&2
  exit 1
fi
echo "drift-gate input parity OK ($(echo "$infra" | grep -c .) TF_VARs consistent across 4 layers)"
