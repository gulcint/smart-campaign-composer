#!/usr/bin/env bash
# Claude Code PreToolUse hook for Bash (docs/PLAN.md §2.3).
# Blocks agents from: pushing to main, force/mirror/all pushes, skipping or
# disabling git hooks, and merging a pull request unless its ci-ok check is green.
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

# Agents may merge a pull request only when its `ci-ok` check is green (owner decision,
# 2026-10-26). Fails closed: unreadable CI status blocks. The ruleset on main enforces
# the same server-side; this layer refuses before the request is even sent.
check_pr_merge() {
  local pr="" squash=0 word out
  for word in "$@"; do
    case "$word" in
      --admin) block "gh pr merge --admin bypasses the main ruleset" ;;
      --squash|-s) squash=1 ;;
      -*) ;;
      *) [[ -z "$pr" ]] && pr="$word" ;;
    esac
  done
  [[ "$pr" =~ ^[0-9]+$ ]] || block "pass the pull request number explicitly: gh pr merge <number> --squash"
  (( squash )) || block "main accepts squash merges only: add --squash"
  out=$(cd "${CLAUDE_PROJECT_DIR:-.}" && gh pr checks "$pr" --json name,bucket 2>/dev/null) || true
  [[ "$(jq -r '[.[]? | select(.name == "ci-ok") | .bucket] | first // ""' <<<"${out:-[]}" 2>/dev/null)" == "pass" ]] \
    || block "ci-ok is not green on PR #$pr (or its status could not be read); wait for CI"
}

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
        check_pr_merge "${words[@]:i+3}"
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

  # Commands that throw away uncommitted or unmerged work. The safe forms stay allowed
  # (worktree remove without --force refuses a dirty tree; branch -d refuses an unmerged branch).
  local rest=" ${words[*]:i} "
  case "$sub" in
    worktree)
      if [[ "${words[i]:-}" == "remove" && "$rest" =~ [[:space:]](--force|-[A-Za-z]*f[A-Za-z]*)[[:space:]] ]]; then
        block "git worktree remove --force deletes uncommitted files; use scripts/worktree-remove.sh <path>"
      fi ;;
    branch)
      [[ "$rest" =~ [[:space:]](-D|--force|-[A-Za-z]*f[A-Za-z]*)[[:space:]] ]] \
        && block "git branch -D/--force deletes unmerged commits; use git branch -d"
      [[ "$rest" =~ [[:space:]]-[A-Za-z]*D[A-Za-z]*[[:space:]] ]] && block "git branch -D deletes unmerged commits; use git branch -d" ;;
    clean)
      [[ "$rest" =~ [[:space:]](--force|-[A-Za-z]*f[A-Za-z]*)[[:space:]] ]] \
        && block "git clean -f deletes untracked files for good; list them with git clean -n and remove by hand" ;;
    reset)
      [[ "$rest" =~ [[:space:]]--hard[[:space:]] ]] && block "git reset --hard discards uncommitted work; use --soft/--mixed or git stash" ;;
    checkout|switch)
      [[ "$rest" =~ [[:space:]](--force|--discard-changes|-[A-Za-z]*f[A-Za-z]*)[[:space:]] ]] \
        && block "git $sub --force discards uncommitted work"
      [[ "$sub" == "checkout" && "$rest" =~ [[:space:]]--[[:space:]] ]] \
        && block "git checkout -- <path> discards uncommitted edits; use git stash" ;;
    restore)
      # Only index-only restore (--staged without --worktree) is non-destructive.
      if ! { [[ "$rest" =~ [[:space:]]--staged[[:space:]] || "$rest" =~ [[:space:]]-S[[:space:]] ]] && ! [[ "$rest" =~ [[:space:]](--worktree|-W)[[:space:]] ]]; }; then
        block "git restore overwrites working-tree files; use git stash"
      fi ;;
    stash)
      [[ "${words[i]:-}" =~ ^(drop|clear)$ ]] && block "git stash drop/clear loses stashed work for good" ;;
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
