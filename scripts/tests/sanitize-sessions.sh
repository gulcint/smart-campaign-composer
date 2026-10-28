#!/usr/bin/env bash
# Behaviour tests for scripts/sanitize-sessions.sh and scripts/hooks/check-ai-sessions.sh:
# plain secrets, and the home-path leaks that line wrapping and slugs cause.
# Uses a made-up account name, never the real one. Run: make test-hooks
set -uo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
fails=0
name=fakeacct

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp" || exit 1
git init -q -b main . && git config user.email t@example.com && git config user.name t
mkdir ai-sessions

ok()  { echo "PASS: $1"; }
bad() { echo "FAIL: $1"; fails=$((fails + 1)); }

sanitize() { USER=$name SANITIZE_EXTRA_NAMES="" SANITIZE_EXTRA_PHRASES="Fake Person" "$root/scripts/sanitize-sessions.sh" "$@" >/dev/null; }
check()    { git add -A >/dev/null 2>&1; CI=1 SANITIZE_EXTRA_NAMES="" SANITIZE_EXTRA_PHRASES="Fake Person" "$root/scripts/hooks/check-ai-sessions.sh" >/dev/null 2>&1; }
check_named() { git add -A >/dev/null 2>&1; CI="" USER=$name SANITIZE_EXTRA_NAMES="" SANITIZE_EXTRA_PHRASES="Fake Person" "$root/scripts/hooks/check-ai-sessions.sh" >/dev/null 2>&1; }

# case <label> <input-text>: the checker must flag it before, the sanitizer must clear it after
case_leak() {
  local label=$1 text=$2 f=ai-sessions/x.md
  printf '%s\n' "$text" > $f
  if check && check_named; then bad "$label: not detected before sanitizing"; else ok "$label: detected"; fi
  sanitize $f
  if grep -q "$name" $f; then bad "$label: account name survived: $(grep -n "$name" $f | head -1)"; else ok "$label: masked"; fi
  if check && check_named; then ok "$label: clean afterwards"; else bad "$label: checker still fails after sanitizing"; fi
  rm -f $f
}

case_leak "plain path"        "see /Users/$name/Desktop/App/file.go for details"
case_leak "wrapped path"      "$(printf 'file at `/Users/\n%s/Desktop/App/file.go`' "$name")"
case_leak "wrapped, indented" "$(printf '    text `/Users/\n         +%s/Desktop/App`' "$name")"
case_leak "wrapped, box"      "$(printf 'x `/Users/\n  │ %s/Desktop/App`' "$name")"
case_leak "claude slug"       "Read ../private/tmp/claude-501/-Users-$name-Desktop-App/tasks/x.output"
case_leak "bare account name" "the user $name asked for it"
case_leak "api key"           "key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789"
case_leak "personal email"    "mail me at someone@personal-mail.dev please"

# full names: the checker and sanitizer match them in any case, across a line wrap
f=ai-sessions/name.md
printf '%s\n' "written by fake person, also FAKE" "  PERSON and Fake Person." > $f
if check; then bad "full name: not detected before sanitizing"; else ok "full name: detected"; fi
sanitize $f
if grep -qi "fake" $f; then bad "full name survived: $(grep -ni fake $f | head -1)"; else ok "full name: masked"; fi
if check; then ok "full name: clean afterwards"; else bad "full name: checker still fails after sanitizing"; fi
rm -f $f

# things that must survive untouched
f=ai-sessions/keep.md
printf '%s\n' "path /Users/<user>/x, bot noreply@anthropic.com, mail a@example.com, handle fakeacct2, runner and users" > $f
cp $f "$tmp/before"
sanitize $f
if diff -q "$tmp/before" $f >/dev/null; then ok "placeholders, allowlisted emails and look-alike words untouched"; else bad "sanitizer changed allowed text: $(diff "$tmp/before" $f | head -3)"; fi
rm -f $f

echo
if (( fails )); then echo "$fails test(s) failed"; exit 1; fi
echo "all session-sanitizer tests passed"
