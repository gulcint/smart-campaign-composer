#!/usr/bin/env bash
# Rejects files that must never be committed (docs/PLAN.md §1.5, §6 #1).
# Usage: check-forbidden-files.sh                 # staged files (pre-commit)
#        check-forbidden-files.sh --all           # every tracked file (CI)
#        check-forbidden-files.sh --range <base>  # every file touched by any commit in <base>..HEAD (CI, MRs)
set -euo pipefail

case "${1:-}" in
  --all)   files=$(git ls-files) ;;
  --range) files=$(git log --name-only --format= "$2..HEAD" | sort -u) ;;
  *)       files=$(git diff --cached --name-only --diff-filter=ACMR) ;;
esac

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
