#!/usr/bin/env bash
# Rejects new test files named after a review round instead of a feature/REQ area
# (docs/PLAN.md §4.5 "Test dosyası düzeni"). Only added or renamed files are checked,
# so existing files can still be edited or merged away.
# Usage: check-test-files.sh                 # staged files (pre-commit)
#        check-test-files.sh --range <base>  # files added in <base>...HEAD
#        check-test-files.sh -- <path>...    # explicit paths (tests)
set -euo pipefail

case "${1:-}" in
  --range) files=$(git diff --name-only --diff-filter=AR "$2...HEAD") ;;
  --)      shift; files=$(printf '%s\n' "$@") ;;
  *)       files=$(git diff --cached --name-only --diff-filter=AR) ;;
esac

# A round/fix/review token, delimited by _ . - or the name's edges: content_round2_test.go,
# fix_test.go, approve.review.spec.ts. Words that merely contain them (prefix, background) pass.
bad_re='(^|[_.-])(round|fix|fixes|review)[0-9]*([_.-]|$)'
test_re='(_test\.go|\.spec\.ts|\.test\.ts)$'

bad=()
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  base=$(basename "$f")
  [[ "$base" =~ $test_re ]] || continue
  [[ "$base" =~ $bad_re ]] && bad+=("$f")
done <<< "$files"

if (( ${#bad[@]} > 0 )); then
  echo "test files are named by feature/REQ area, never by review round (docs/PLAN.md §4.5):" >&2
  printf '  %s\n' "${bad[@]}" >&2
  echo "add the cases as new rows/subtests in the existing file for that area instead" >&2
  exit 1
fi
