#!/usr/bin/env bash
# Rejects files that must never be committed (docs/PLAN.md §1.5, §6 #1).
# Usage: check-forbidden-files.sh            # staged files (pre-commit)
#        check-forbidden-files.sh --all      # every tracked file (CI)
set -euo pipefail

if [[ "${1:-}" == "--all" ]]; then
  files=$(git ls-files)
else
  files=$(git diff --cached --name-only --diff-filter=ACMR)
fi

bad=()
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  base=$(basename "$f")
  case "$base" in
    .env.example) continue ;;
    .env|.env.*|*.pem|*.key|"Smart Campaign Composer Case.html")
      bad+=("$f") ;;
  esac
  case "$f" in
    pgdata/*|*/pgdata/*) bad+=("$f") ;;
  esac
done <<< "$files"

if (( ${#bad[@]} > 0 )); then
  echo "forbidden files must not be committed:" >&2
  printf '  %s\n' "${bad[@]}" >&2
  exit 1
fi
