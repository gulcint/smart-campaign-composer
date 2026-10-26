#!/usr/bin/env bash
# Creates the test writer's isolated worktree (docs/PLAN.md §1.2, §3.1):
# a sparse checkout without implementation directories, on a new test/* branch.
# Usage: scripts/test-writer-worktree.sh <base-ref> <test-branch>
#   e.g. scripts/test-writer-worktree.sh origin/slice/p3-audience test/p3-audience-rules
# Then:  cd <printed path> && claude --agent test-writer
set -euo pipefail

base="${1:?base ref, e.g. origin/slice/p3-audience}"
branch="${2:?test branch, e.g. test/p3-audience-rules}"

[[ "$branch" == test/* ]] || { echo "test branch must start with test/" >&2; exit 1; }
scripts/hooks/check-branch-name.sh "$branch"

root=$(git rev-parse --show-toplevel)
dir="$(dirname "$root")/$(basename "$root")-${branch//\//-}"

git worktree add --no-checkout -b "$branch" "$dir" "$base"
git -C "$dir" sparse-checkout set --no-cone \
  '/*' \
  '!/cmd/' '!/internal/' '!/migrations/' '!/web/src/' \
  '**/*_test.go' '**/testdata/' '/web/tests/'
git -C "$dir" checkout -q "$branch"
(cd "$dir" && lefthook install >/dev/null && git config commit.template .gitmessage)

# Fail loudly if any implementation file made it into the worktree.
leaked=$(cd "$dir" && find cmd internal migrations web/src -type f ! -name '*_test.go' ! -path '*/testdata/*' 2>/dev/null || true)
if [[ -n "$leaked" ]]; then
  echo "implementation files present in the test worktree:" >&2
  printf '  %s\n' $leaked >&2
  exit 1
fi

echo "test worktree ready: $dir"
echo "case document (gitignored, not in the worktree): $root/Smart Campaign Composer Case.html"
echo "next: cd \"$dir\" && claude --agent test-writer"
