#!/usr/bin/env bash
# PreToolUse hook scoped to the test-writer agent (.claude/agents/test-writer.md).
# Enforces docs/PLAN.md §3.1: the test writer never sees implementation and
# only writes test files. Layered with the sparse worktree, CI role-paths and review.
#   Read / Grep         : no implementation paths; Grep must be scoped to allowed roots or *_test.go
#   Glob                : allowed (file names only, no contents)
#   Write / Edit        : test paths only
#   Bash                : allowlist of git / file-listing commands that cannot print implementation
# Exit 2 = block; stderr is shown to the agent.
set -euo pipefail
set -f

input=$(cat)
tool=$(jq -r '.tool_name // empty' <<<"$input")
block() { echo "blocked by scripts/claude/test-writer-guard.sh: $1 (docs/PLAN.md §3.1)" >&2; exit 2; }

impl_re='(^|/)(cmd|internal|migrations)(/|$)|(^|/)web/src(/|$)'
test_re='(_test\.go$|(^|/)testdata(/|$)|(^|/)test/|(^|/)web/tests(/|$)|\.spec\.ts$)'

is_impl() { [[ "$1" =~ $impl_re ]] && ! [[ "$1" =~ $test_re ]]; }
is_test() { [[ "$1" =~ $test_re ]]; }

case "$tool" in
  Read)
    p=$(jq -r '.tool_input.file_path // empty' <<<"$input")
    is_impl "$p" && block "reading implementation file '$p'"
    ;;

  Grep)
    p=$(jq -r '.tool_input.path // empty' <<<"$input")
    g=$(jq -r '.tool_input.glob // empty' <<<"$input")
    [[ -n "$p" ]] && is_impl "$p" && block "searching implementation path '$p'"
    # Unscoped or root-level searches would read implementation contents.
    allowed_root='(^|/)(docs|test|testdata|web/tests|\.claude)(/|$)|\.html$|\.md$'
    if ! { [[ -n "$p" && "$p" =~ $allowed_root ]] || [[ "$g" == *_test.go || "$g" == *.spec.ts ]]; }; then
      block "Grep must target docs/, test/, testdata/, web/tests/, .claude/ or use glob '*_test.go'"
    fi
    ;;

  Glob) ;;

  Write|Edit|MultiEdit|NotebookEdit)
    p=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' <<<"$input")
    is_test "$p" || block "the test writer may only write test files (*_test.go, test/, testdata/, web/tests/), not '$p'"
    ;;

  Bash)
    cmd=$(jq -r '.tool_input.command // empty' <<<"$input")
    while IFS= read -r seg; do
      seg="${seg#"${seg%%[![:space:]]*}"}"
      [[ -z "$seg" ]] && continue
      # shellcheck disable=SC2206
      w=($seg)
      case "${w[0]}" in
        ls|pwd|mkdir|gofmt|echo|wc) ;;
        git)
          case "${w[1]:-}" in
            status|add|commit|push|branch|switch|fetch|worktree|rev-parse) ;;
            log)
              [[ "$seg" =~ (^|[[:space:]])(-p|--patch|-u|--stat|--name-only|--name-status|--follow)([[:space:]]|$) ]] \
                && block "git log may not show diffs or file lists"
              ;;
            diff)
              [[ "$seg" =~ (^|[[:space:]])--cached([[:space:]]|$) ]] || block "only 'git diff --cached' (your own staged tests) is allowed"
              ;;
            sparse-checkout)
              [[ "${w[2]:-}" == "list" ]] || block "the sparse checkout is set up by scripts/test-writer-worktree.sh, not changed by the test writer"
              ;;
            *) block "git ${w[1]:-} can expose implementation from the object store" ;;
          esac
          ;;
        *) block "command '${w[0]}' is not on the test writer's allowlist (use Read/Glob/Write/Edit)" ;;
      esac
    done < <(printf '%s\n' "$cmd" | tr ';&|' '\n\n\n')
    ;;
esac
exit 0
