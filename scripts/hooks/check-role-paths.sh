#!/usr/bin/env bash
# Role separation (docs/PLAN.md §3.1 item 4):
#   test/*            may not touch implementation paths
#   feat|fix|refactor may not touch test paths
# Usage: check-role-paths.sh [--warn] [<branch> <base-ref>]
#   --warn          report but exit 0 (pre-push); CI runs without it
#   <branch>        defaults to the current branch
#   <base-ref>      defaults to origin/slice/p<N>-* for the branch's phase, else origin/main
set -euo pipefail

warn=0
if [[ "${1:-}" == "--warn" ]]; then warn=1; shift; fi

branch="${1:-$(git rev-parse --abbrev-ref HEAD)}"
base="${2:-}"

if [[ -z "$base" ]]; then
  phase=$(printf '%s' "$branch" | sed -nE 's#^[a-z]+/(p[0-9]+)-.*#\1#p')
  if [[ -n "$phase" ]]; then
    base=$(git for-each-ref --format='%(refname:short)' "refs/remotes/origin/slice/${phase}-*" | head -n1)
  fi
  base="${base:-origin/main}"
fi

if ! git rev-parse --verify --quiet "$base" >/dev/null; then
  echo "role-paths: base '$base' not found, skipping" >&2
  exit 0
fi

impl_re='^(cmd/|internal/|migrations/|web/src/)'
test_re='(_test\.go$|^test/|(^|/)testdata/|^web/tests/)'

changed=$(git diff --name-only "$(git merge-base "$base" HEAD)" HEAD)

case "$branch" in
  test/*)                 offending=$(printf '%s\n' "$changed" | grep -E "$impl_re" | grep -Ev "$test_re" || true); role="test writer"; area="implementation" ;;
  feat/*|fix/*|refactor/*) offending=$(printf '%s\n' "$changed" | grep -E "$test_re" || true); role="implementer"; area="test" ;;
  *) exit 0 ;;
esac

if [[ -n "$offending" ]]; then
  echo "role-paths: $role branch '$branch' changes $area files:" >&2
  printf '  %s\n' $offending >&2
  (( warn )) && exit 0
  exit 1
fi
