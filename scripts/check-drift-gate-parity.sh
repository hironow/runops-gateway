#!/usr/bin/env bash
# Assert TF_VAR parity across the three layers that feed the tofu drift gate
# (ADR 0043). A var present in the infra Apply job but missing downstream falls
# back to the action's default "" and shows as false-positive drift, which
# blocks every deploy. The three layers:
#
#   (a) .github/workflows/cd.yaml      infra job       TF_VAR_* env  (source of truth)
#   (b) .github/workflows/cd.yaml      drift-gate job  with: inputs  (mapped to TF_VAR names)
#   (c) .github/actions/tofu-drift-gate/action.yaml    TF_VAR_* env
#
# Only TF_VAR_github_repo (WIF restriction) crosses; the github provider's
# owner/name/token are deliberately never passed (token-free plan), so they
# are absent from all three layers by design.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Paths default to the real files; tests override them with fixtures.
CD="${1:-$ROOT/.github/workflows/cd.yaml}"
ACTION="${2:-$ROOT/.github/actions/tofu-drift-gate/action.yaml}"

# (a) infra job: every TF_VAR_* it sets. Match key lines (`<indent>TF_VAR_x:`)
# only, so prose mentions of TF_VAR_* are not counted.
infra=$(awk '/^  infra:/{f=1} /^  drift-gate:/{f=0} f' "$CD" \
  | grep -E '^[[:space:]]+TF_VAR_[a-z0-9_]+:' | grep -oE 'TF_VAR_[a-z0-9_]+' | sort -u)

# (c) action: every TF_VAR_* the drift plan step exports (key lines only, so
# input descriptions that mention TF_VAR_* names are not counted).
action=$(grep -E '^[[:space:]]+TF_VAR_[a-z0-9_]+:' "$ACTION" | grep -oE 'TF_VAR_[a-z0-9_]+' | sort -u)

# (b) drift-gate job: the `with:` input keys, mapped to TF_VAR names.
#   wif_provider / service_account : auth, not a tofu var -> skip
#   state_bucket                   : -> TF_VAR_tofu_state_bucket (non-obvious mapping)
#   <key>                          : -> TF_VAR_<key>
with_keys=$(awk '
  /^  drift-gate:/{f=1} /^  deploy:/{f=0; u=0}
  f && /uses: \.\/\.github\/actions\/tofu-drift-gate/{u=1; next}
  f && u && /^          [a-z0-9_]+:/{print}
' "$CD" | sed -E 's/^ *([a-z0-9_]+):.*/\1/' | sort -u)

withset=""
while IFS= read -r k; do
  [ -z "$k" ] && continue
  case "$k" in
    wif_provider | service_account) continue ;;
    state_bucket) withset+="TF_VAR_tofu_state_bucket"$'\n' ;;
    *) withset+="TF_VAR_${k}"$'\n' ;;
  esac
done <<< "$with_keys"
withset=$(printf '%s' "$withset" | grep -E '^TF_VAR_' | sort -u)

fail=0
report() {
  local label="$1" diff="$2"
  if [ -n "$diff" ]; then
    echo "MISMATCH ($label):" >&2
    echo "$diff" | sed 's/^/  /' >&2
    fail=1
  fi
}
report "infra TF_VAR vs drift-gate with:" "$(comm -3 <(echo "$infra") <(echo "$withset"))"
report "infra TF_VAR vs action TF_VAR" "$(comm -3 <(echo "$infra") <(echo "$action"))"

if [ "$fail" -ne 0 ]; then
  echo "drift-gate input parity check FAILED — keep cd.yaml infra/drift-gate and action.yaml in lockstep." >&2
  exit 1
fi
echo "drift-gate input parity OK ($(echo "$infra" | grep -c .) TF_VARs consistent across 3 layers)"
