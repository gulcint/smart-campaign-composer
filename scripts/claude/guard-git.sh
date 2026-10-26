#!/usr/bin/env bash
# Claude Code PreToolUse hook for Bash (docs/PLAN.md §2.3).
# Blocks agents from: pushing to main, plain force pushes, skipping git hooks,
# and merging merge requests (the owner merges, docs/PLAN.md §3).
# Each shell segment (split on newlines, ; & |) is checked on its own, so text
# inside heredocs or commit messages does not count as a flag.
# Exit 2 = block; stderr is shown to the agent.
set -euo pipefail

cmd=$(jq -r '.tool_input.command // empty')
[[ -z "$cmd" ]] && exit 0

block() { echo "blocked by scripts/claude/guard-git.sh: $1" >&2; exit 2; }

branch=$(git -C "${CLAUDE_PROJECT_DIR:-.}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)

check_segment() {
  local seg="$1"
  # Strip leading whitespace and env assignments / "cd x &&" leftovers are separate segments.
  seg="${seg#"${seg%%[![:space:]]*}"}"

  if [[ "$seg" =~ ^git([[:space:]]+-C[[:space:]]+[^[:space:]]+)?[[:space:]]+(commit|push|merge|rebase|cherry-pick)([[:space:]]|$) ]]; then
    for word in $seg; do
      [[ "$word" == "--no-verify" ]] && block "--no-verify bypasses the project hooks"
    done
  fi

  if [[ "$seg" =~ ^git([[:space:]]+-C[[:space:]]+[^[:space:]]+)?[[:space:]]+push([[:space:]]|$) ]]; then
    local positional=0 word
    for word in ${seg#*push}; do
      case "$word" in
        -f|--force) block "use --force-with-lease instead of --force" ;;
        -*) ;;
        *)
          positional=$((positional + 1))
          if [[ "$word" =~ (^|:)(refs/heads/)?main$ ]]; then
            block "pushing to main is not allowed; push a branch and open a merge request"
          fi
          ;;
      esac
    done
    # Without "<remote> <refspec>" git pushes the current branch; on main that updates main.
    if [[ "$branch" == "main" ]] && (( positional < 2 )); then
      block "current branch is main; a push without an explicit refspec would update main"
    fi
  fi

  if [[ "$seg" =~ ^glab[[:space:]]+mr[[:space:]]+(merge|accept)([[:space:]]|$) ]]; then
    block "merge requests are merged by the owner in the GitLab UI"
  fi
}

while IFS= read -r seg; do
  check_segment "$seg"
done < <(printf '%s\n' "$cmd" | tr ';&|' '\n\n\n')

exit 0
