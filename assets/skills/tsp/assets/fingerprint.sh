#!/usr/bin/env bash
# Content fingerprint for plan baselines and finding verdicts.
# Usage: fingerprint.sh [--root <repo>] [paths...]
#   no paths  -> fingerprint of every tracked + untracked (non-ignored) file in the repo, except the
#                plan (.tpp.json "planPath", else the pre-rename .rdd-plus.json "planPath", else
#                docs/testing/test-plan.md): the fingerprint is written into the plan, so hashing
#                the plan would make every baseline stale
#   paths     -> fingerprint of exactly those files (a finding's cited files)
# Output: sha256 hex over "path<TAB>sha256(content)" lines, sorted; deterministic across machines.
set -euo pipefail
ROOT="."
if [[ "${1:-}" == "--root" ]]; then ROOT="$2"; shift 2; fi
cd "$ROOT"
if [[ $# -eq 0 ]]; then
  PLAN="docs/testing/test-plan.md"
  DECLARATION=""
  if [[ -f .tpp.json ]]; then DECLARATION=.tpp.json; elif [[ -f .rdd-plus.json ]]; then DECLARATION=.rdd-plus.json; fi
  if [[ -n "$DECLARATION" ]]; then
    declared="$(tr -d '\r\n' < "$DECLARATION" | sed -n 's/.*"planPath"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
    [[ -n "$declared" ]] && PLAN="$declared"
  fi
  while [[ "$PLAN" == ./* ]]; do PLAN="${PLAN#./}"; done
  [[ -f "$PLAN" ]] || echo "fingerprint.sh: plan $PLAN not found, nothing excluded" >&2
  git ls-files -co --exclude-standard -z | { grep -zvxF -- "$PLAN" || [[ $? -eq 1 ]]; }
else
  printf '%s\0' "$@"
fi | sort -z | while IFS= read -r -d '' f; do
  [[ -f "$f" ]] || continue
  printf '%s\t%s\n' "$f" "$(sha256sum -- "$f" | cut -c1-64)"
done | sha256sum | cut -c1-64
