#!/usr/bin/env bash
# Claude Code PreToolUse hook for Bash (docs/PLAN.md §2.3).
# Blocks agents from: pushing to main, force/mirror/all pushes, skipping or
# disabling git hooks, and merging pull requests (the owner merges, §3).
# Each shell segment (split on newlines, ; & |) is checked on its own, so text
# inside heredocs or commit messages does not count as a flag.
# This is a fast first layer; the GitHub ruleset on main is the backstop.
# Exit 2 = block; stderr is shown to the agent.
set -euo pipefail
set -f  # commands are split into words below; never glob-expand them

cmd=$(jq -r '.tool_input.command // empty')
[[ -z "$cmd" ]] && exit 0

block() { echo "blocked by scripts/claude/guard-git.sh: $1" >&2; exit 2; }

branch=$(git -C "${CLAUDE_PROJECT_DIR:-.}" rev-parse --abbrev-ref HEAD 2>/dev/null || true)

check_segment() {
  local seg="$1" word
  local -a words

  # Disabling lefthook through the environment is a hook bypass wherever it appears.
  [[ "$seg" =~ (^|[[:space:]])LEFTHOOK(_EXCLUDE)?= ]] && block "LEFTHOOK env vars disable the project hooks"

  # shellcheck disable=SC2206
  words=($seg)
  local i=0 n=${#words[@]}

  # Skip env assignments and wrappers: VAR=x, env, command, exec.
  while (( i < n )); do
    case "${words[i]}" in
      *=*|env|command|exec) i=$((i + 1)) ;;
      *) break ;;
    esac
  done
  (( i < n )) || return 0

  # Nested shells: check the -c script as its own command.
  case "${words[i]}" in
    bash|sh|zsh)
      if [[ "${words[i+1]:-}" == "-c" ]]; then
        local inner="${seg#*-c}"
        inner="${inner//\"/}"; inner="${inner//\'/}"
        check_all "$inner"
      fi
      return 0 ;;
    gh)
      if [[ "${words[i+1]:-}" == "pr" && "${words[i+2]:-}" == "merge" ]]; then
        block "pull requests are merged by the owner in the GitHub UI"
      fi
      return 0 ;;
    git) i=$((i + 1)) ;;
    *) return 0 ;;
  esac

  # git global options before the subcommand: -C <path>, -c <k=v>, --opt[=v].
  while (( i < n )); do
    case "${words[i]}" in
      -C) i=$((i + 2)) ;;
      -c)
        [[ "${words[i+1]:-}" =~ ^core\.hooks[Pp]ath= ]] && block "overriding core.hooksPath disables the project hooks"
        i=$((i + 2)) ;;
      -*) i=$((i + 1)) ;;
      *) break ;;
    esac
  done
  (( i < n )) || return 0

  local sub="${words[i]}"
  i=$((i + 1))

  case "$sub" in
    commit|push|merge|rebase|cherry-pick|am)
      for word in "${words[@]:i}"; do
        [[ "$word" == "--no-verify" ]] && block "--no-verify bypasses the project hooks"
      done ;;
    config)
      [[ "${words[*]:i}" =~ core\.hooks[Pp]ath ]] && block "changing core.hooksPath disables the project hooks" ;;
  esac

  if [[ "$sub" == "commit" ]]; then
    for word in "${words[@]:i}"; do
      # -n is --no-verify for commit; also inside clusters like -nm.
      [[ "$word" =~ ^-[A-Za-z]*n[A-Za-z]*$ ]] && block "-n (--no-verify) bypasses the project hooks"
    done
  fi

  if [[ "$sub" == "push" ]]; then
    local positional=0
    for word in "${words[@]:i}"; do
      case "$word" in
        --force|--all|--mirror) block "$word is not allowed; use --force-with-lease on your own branch" ;;
        --*) ;;
        -*)
          [[ "$word" == *f* ]] && block "use --force-with-lease instead of -f"
          ;;
        *)
          positional=$((positional + 1))
          [[ "$word" == +* ]] && block "'+' refspecs force-push; use --force-with-lease"
          [[ "$word" =~ (^|:)(refs/heads/)?main$ ]] && block "pushing to main is not allowed; push a branch and open a pull request"
          ;;
      esac
    done
    # Without "<remote> <refspec>" git pushes the current branch; on main that updates main.
    if [[ "$branch" == "main" ]] && (( positional < 2 )); then
      block "current branch is main; a push without an explicit refspec would update main"
    fi
  fi
}

check_all() {
  local seg
  while IFS= read -r seg; do
    check_segment "$seg"
  done < <(printf '%s\n' "$1" | tr ';&|' '\n\n\n')
}

check_all "$cmd"
exit 0
