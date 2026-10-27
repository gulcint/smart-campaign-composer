#!/usr/bin/env bash
# Removes a linked git worktree only when nothing in it can be lost.
# `git worktree remove --force` is blocked for agents (scripts/claude/guard-git.sh);
# this is the sanctioned way. It refuses when the worktree holds
#   - staged or unstaged changes, or untracked files,
#   - ignored files (.env, build output, ...) unless --allow-ignored is passed,
#   - or the path is the main worktree.
# Commits are never lost: the branch stays; unpushed commits are reported.
# Usage: scripts/worktree-remove.sh [--allow-ignored] <worktree-path>
set -euo pipefail

allow_ignored=0
if [[ "${1:-}" == "--allow-ignored" ]]; then allow_ignored=1; shift; fi
path="${1:?usage: scripts/worktree-remove.sh [--allow-ignored] <worktree-path>}"
[[ -d "$path" ]] || { echo "worktree-remove: '$path' is not a directory" >&2; exit 1; }

path=$(cd "$path" && pwd -P)
main=$(git rev-parse --show-toplevel)
main=$(cd "$main" && pwd -P)

[[ "$path" != "$main" ]] || { echo "worktree-remove: refusing to remove the main worktree" >&2; exit 1; }
registered=$(git worktree list --porcelain | sed -n 's/^worktree //p' | while IFS= read -r wt; do
  if [[ "$(cd "$wt" 2>/dev/null && pwd -P)" == "$path" ]]; then echo yes; fi
done)
[[ "$registered" == "yes" ]] || { echo "worktree-remove: '$path' is not a registered worktree" >&2; exit 1; }

dirty=$(git -C "$path" status --porcelain --untracked-files=all)
if [[ -n "$dirty" ]]; then
  echo "worktree-remove: '$path' has uncommitted or untracked files; commit or move them first:" >&2
  printf '%s\n' "$dirty" | sed 's/^/  /' >&2
  exit 1
fi

if (( ! allow_ignored )); then
  ignored=$(git -C "$path" status --porcelain --ignored --untracked-files=all | sed -n 's/^!! //p')
  if [[ -n "$ignored" ]]; then
    echo "worktree-remove: '$path' has ignored files that would be deleted (pass --allow-ignored if they are disposable):" >&2
    printf '%s\n' "$ignored" | sed 's/^/  /' >&2
    exit 1
  fi
fi

unpushed=$(git -C "$path" rev-list HEAD --not --remotes 2>/dev/null | wc -l | tr -d ' ')
(( unpushed == 0 )) || echo "worktree-remove: note: $unpushed commit(s) on this branch are not on any remote (the branch is kept)" >&2

git worktree remove "$path"
echo "worktree-remove: removed $path"
