#!/usr/bin/env bash
# Branch policy (docs/PLAN.md §1.3).
# Usage: check-branch-name.sh <branch>          # validate one name (CI)
#        check-branch-name.sh --pre-push        # read refs from stdin (git pre-push)
set -euo pipefail

pattern='^(slice|test|feat|fix|refactor|docs|chore|ci|sec)/p[0-9]+-[a-z0-9]+(-[a-z0-9]+)*$'

check_name() {
  local name="$1"
  if [[ ! "$name" =~ $pattern ]]; then
    echo "branch '$name' does not match <type>/p<phase>-<kebab-desc>" >&2
    return 1
  fi
  if (( ${#name} > 50 )); then
    echo "branch '$name' is ${#name} chars, max 50" >&2
    return 1
  fi
}

if [[ "${1:-}" != "--pre-push" ]]; then
  check_name "$1"
  exit
fi

status=0
# git pre-push stdin: <local ref> <local sha> <remote ref> <remote sha>
while read -r local_ref _ remote_ref _; do
  [[ -z "${remote_ref:-}" ]] && continue
  case "$remote_ref" in
    refs/heads/main)
      echo "direct push to main is not allowed; open a merge request" >&2
      status=1 ;;
    refs/heads/*)
      check_name "${remote_ref#refs/heads/}" || status=1 ;;
  esac
done
exit $status
