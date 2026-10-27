#!/usr/bin/env bash
# Behaviour tests for the destructive-git rules of scripts/claude/guard-git.sh and for
# scripts/worktree-remove.sh. Commands below are data fed to the guard, never executed.
# Run: make test-hooks
set -uo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
guard="$root/scripts/claude/guard-git.sh"
fails=0

verdict() { # <command> -> "block" | "allow"
  local json
  json=$(jq -n --arg c "$1" '{tool_input:{command:$c}}')
  if CLAUDE_PROJECT_DIR="$root" "$guard" <<<"$json" >/dev/null 2>&1; then echo allow; else echo block; fi
}
expect() { # <block|allow> <command>
  local got; got=$(verdict "$2")
  if [[ "$got" == "$1" ]]; then echo "PASS: $1  $2"; else echo "FAIL: expected $1, got $got: $2"; fails=$((fails + 1)); fi
}

# --- must be blocked: throw away uncommitted or unmerged work ---
for c in \
  'git worktree remove --force ../wt' \
  'git worktree remove -f ../wt' \
  'git worktree remove -ff ../wt' \
  'git worktree remove ../wt --force' \
  'git -C /repo worktree remove --force ../wt' \
  'git branch -D test/p2-x' \
  'git branch --delete --force test/p2-x' \
  'git branch -d -f test/p2-x' \
  'git clean -fd' \
  'git clean -f' \
  'git clean --force -x' \
  'git reset --hard' \
  'git reset --hard origin/main' \
  'git checkout -f main' \
  'git checkout --force main' \
  'git checkout -- .' \
  'git checkout main -- internal/x.go' \
  'git switch -f main' \
  'git switch --discard-changes main' \
  'git restore internal/x.go' \
  'git restore --worktree --staged internal/x.go' \
  'git stash drop' \
  'git stash clear' \
  'cd /repo && git worktree remove --force ../wt' \
  'env FOO=1 git reset --hard' \
  'bash -c "git worktree remove --force ../wt"'; do
  expect block "$c"
done

# --- must stay allowed: the safe forms ---
for c in \
  'git worktree remove ../wt' \
  'git worktree list' \
  'git worktree add ../wt -b test/p3-x origin/main' \
  'git branch -d test/p2-x' \
  'git branch --list' \
  'git clean -n' \
  'git clean --dry-run -d' \
  'git reset --soft HEAD~1' \
  'git reset HEAD internal/x.go' \
  'git checkout main' \
  'git checkout -b feat/p3-x' \
  'git switch main' \
  'git switch -c feat/p3-x' \
  'git restore --staged internal/x.go' \
  'git stash' \
  'git stash list' \
  'git stash pop' \
  'git status --short' \
  'scripts/worktree-remove.sh ../wt' \
  'git commit -m "docs: mention git reset --hard in text"'; do
  expect allow "$c"
done

# --- the test writer's own guard (scripts/claude/test-writer-guard.sh) ---
tw_verdict() {
  local json
  json=$(jq -n --arg c "$1" '{tool_name:"Bash",tool_input:{command:$c}}')
  if "$root/scripts/claude/test-writer-guard.sh" <<<"$json" >/dev/null 2>&1; then echo allow; else echo block; fi
}
tw_expect() {
  local got; got=$(tw_verdict "$2")
  if [[ "$got" == "$1" ]]; then echo "PASS: test-writer $1  $2"; else echo "FAIL: test-writer expected $1, got $got: $2"; fails=$((fails + 1)); fi
}
for c in 'git worktree remove ../wt' 'git worktree remove --force ../wt' 'git worktree add ../x' \
         'git branch -D test/p2-x' 'git branch -d test/p2-x' 'git branch -m old new' 'git switch -f main'; do
  tw_expect block "$c"
done
for c in 'git worktree list' 'git branch' 'git branch --list' 'git switch main' 'git status' 'git add .'; do
  tw_expect allow "$c"
done

# Shell grep/rg: test paths and docs only (owner decision, docs/PLAN.md §3.1).
for c in 'grep -rn REQ-CNT-01 test web/tests' 'grep -rn REQ-CNT-01 internal/content/validate_test.go' \
         'rg -n REQ-AUD-04 test testdata docs/requirements.md' 'grep -rn -e REQ-SND-03 test' 'grep -c REQ-AI-01 docs/PLAN.md' \
         'grep -rln REQ-CNT-01 ./test/e2e' 'grep -rn x internal/seed/testdata' \
         'grep -rn REQ-CNT-01 test | wc -l'; do
  tw_expect allow "$c"
done
for c in 'grep -rn x internal' 'grep -rn x .' 'grep -rn x' 'grep x' 'grep x web/src/a.ts' 'grep x test ../internal' \
         'grep x test/../internal' 'grep -rn x cmd migrations' 'grep x /etc/passwd' 'grep x .env' 'grep x docs/.env.local' \
         'grep -f internal/a.go test' 'grep -rnf internal/a.go test' 'rg --pre cat x test' 'rg x test --pre=cat' \
         'grep -rn x test $HOME' 'grep x test $(echo internal)' 'grep x - < internal/a.go' 'grep x test | grep y' \
         'grep -e x -- internal' 'rg x *' 'git grep x' \
         'grep -rn x docs/contracts/openapi.yaml' 'grep -rn x docs/contracts' 'grep -rn x docs' 'grep -c x DECISIONS.md' \
         'rg x docs/contracts/ui.md test' 'grep x Smart\ Campaign\ Composer\ Case.html' 'grep x ../Case.html' \
         'cat internal/a.go' 'python3 -c 1' 'echo $(cat internal/a.go)' 'echo `cat internal/a.go`' 'echo x > internal/a.go'; do
  tw_expect block "$c"
done

# Grep tool: only test paths, test globs or docs.
tw_tool() { # <block|allow> <tool_input json>
  local got=block
  if "$root/scripts/claude/test-writer-guard.sh" <<<"{\"tool_name\":\"Grep\",\"tool_input\":$2}" >/dev/null 2>&1; then got=allow; fi
  if [[ "$got" == "$1" ]]; then echo "PASS: test-writer Grep $1  $2"; else echo "FAIL: test-writer Grep expected $1, got $got: $2"; fails=$((fails + 1)); fi
}
tw_tool allow '{"pattern":"REQ-CNT-01","glob":"*_test.go"}'
tw_tool allow '{"pattern":"REQ-CNT-01","path":"web/tests"}'
tw_tool allow '{"pattern":"REQ-CNT-01","path":"test"}'
tw_tool allow '{"pattern":"REQ-CNT-01","glob":"*.spec.ts"}'
tw_tool allow '{"pattern":"REQ-CNT-01","path":"docs/requirements.md"}'
tw_tool block '{"pattern":"x","path":"docs/contracts/openapi.yaml"}'
tw_tool block '{"pattern":"x","path":"docs/contracts"}'
tw_tool block '{"pattern":"x","path":"docs"}'
tw_tool block '{"pattern":"x","path":"DECISIONS.md"}'
tw_tool block '{"pattern":"x","path":"/repo/Smart Campaign Composer Case.html"}'
tw_tool block '{"pattern":"REQ-CNT-01"}'

# Read and Glob: requirements.md and test files only; contracts, DECISIONS.md and the case HTML are blocked.
tw_other() { # <block|allow> <tool> <tool_input json>
  local got=block
  if "$root/scripts/claude/test-writer-guard.sh" <<<"{\"tool_name\":\"$2\",\"tool_input\":$3}" >/dev/null 2>&1; then got=allow; fi
  if [[ "$got" == "$1" ]]; then echo "PASS: test-writer $2 $1  $3"; else echo "FAIL: test-writer $2 expected $1, got $got: $3"; fails=$((fails + 1)); fi
}
tw_other allow Read '{"file_path":"/repo/docs/requirements.md"}'
tw_other allow Read '{"file_path":"/repo/docs/PLAN.md"}'
tw_other allow Read '{"file_path":"/repo/internal/content/validate_test.go"}'
tw_other allow Read '{"file_path":"/repo/test/e2e/content_test.go"}'
tw_other block Read '{"file_path":"/repo/docs/contracts/openapi.yaml"}'
tw_other block Read '{"file_path":"/repo/docs/contracts/ui.md"}'
tw_other block Read '{"file_path":"/repo/DECISIONS.md"}'
tw_other block Read '{"file_path":"/repo/Smart Campaign Composer Case.html"}'
tw_other block Read '{"file_path":"/repo/internal/content/validate.go"}'
tw_other allow Glob '{"pattern":"**/*_test.go"}'
tw_other block Glob '{"pattern":"docs/contracts/**"}'
tw_other block Glob '{"pattern":"DECISIONS.md"}'
tw_other block Glob '{"pattern":"*Case.html"}'
tw_tool block '{"pattern":"x","path":"internal/content"}'
tw_tool block '{"pattern":"x","path":"internal","glob":"*_test.go"}'

# --- scripts/worktree-remove.sh against a scratch repo ---
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp" || exit 1
git init -q -b main repo && cd repo || exit 1
git config user.email t@example.com; git config user.name t
echo base > a.txt; printf 'ignored.log\n' > .gitignore; git add -A; git commit -qm base
remover="$root/scripts/worktree-remove.sh"

mkwt() { git worktree add -q -b "$1" "../$1" main; }
check() { # <name> <expected-exit> <expected-dir-state: gone|kept> <args...>
  local name=$1 want=$2 state=$3; shift 3
  local out; out=$("$remover" "$@" 2>&1); local rc=$?
  local now=kept; [[ -d "../$name" ]] || now=gone
  if [[ $rc -eq $want && "$now" == "$state" ]]; then echo "PASS: worktree-remove $name (exit $rc, $now)"
  else echo "FAIL: worktree-remove $name: exit $rc (want $want), dir $now (want $state): $out"; fails=$((fails + 1)); fi
}

mkwt clean;     check clean 0 gone ../clean
mkwt untracked; echo x > ../untracked/new.txt;            check untracked 1 kept ../untracked
mkwt staged;    echo x > ../staged/new.txt; git -C ../staged add new.txt; check staged 1 kept ../staged
mkwt modified;  echo y >> ../modified/a.txt;              check modified 1 kept ../modified
mkwt ignored;   echo z > ../ignored/ignored.log;          check ignored 1 kept ../ignored
check ignored 0 gone --allow-ignored ../ignored
mkwt unpushed;  echo u > ../unpushed/u.txt; git -C ../unpushed add u.txt; git -C ../unpushed commit -qm u
check unpushed 0 gone ../unpushed
if git rev-parse --verify -q unpushed >/dev/null; then echo "PASS: branch kept after removal"; else echo "FAIL: branch lost"; fails=$((fails + 1)); fi
"$remover" "$tmp/repo" >/dev/null 2>&1 && { echo "FAIL: main worktree removal accepted"; fails=$((fails + 1)); } || echo "PASS: main worktree refused"
mkdir ../plain; "$remover" ../plain >/dev/null 2>&1 && { echo "FAIL: unregistered dir accepted"; fails=$((fails + 1)); } || echo "PASS: unregistered dir refused"

echo
if (( fails )); then echo "$fails test(s) failed"; exit 1; fi
echo "all destructive-git hook tests passed"
