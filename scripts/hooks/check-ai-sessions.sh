#!/usr/bin/env bash
# Scans ai-sessions/ for leftovers the sanitizer should have removed (docs/PLAN.md §7).
# Usage: check-ai-sessions.sh           # staged files under ai-sessions/
#        check-ai-sessions.sh --all     # every tracked file under ai-sessions/ (CI)
set -euo pipefail

if [[ "${1:-}" == "--all" ]]; then
  files=$(git ls-files -- ai-sessions/)
  show() { cat "$1"; }
else
  files=$(git diff --cached --name-only --diff-filter=ACMR -- ai-sessions/)
  show() { git show ":$1"; }
fi
[[ -z "$files" ]] && exit 0

key_re='sk-ant-[A-Za-z0-9_-]{10,}'
home_re='/Users/[A-Za-z0-9._-]+'
slug_re='-Users-[A-Za-z0-9._]+-'
email_re='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
# Allowed: placeholders, RFC 2606 domains, Anthropic's commit trailer address.
allow_re='/Users/<user>|@example\.(com|test|org|net)|noreply@anthropic\.com|\[REDACTED'

names="${SANITIZE_EXTRA_NAMES:-}"
[[ -z "${CI:-}" ]] && names="${USER:-} $names"

status=0
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  hits=$(show "$f" | grep -nEo "$key_re|$home_re|$slug_re|$email_re" | grep -Ev "$allow_re" || true)
  # A home path wrapped across lines: "/Users/" ends a line and the account name starts the next.
  wrapped=$(show "$f" | perl -0777 -ne 'while (m{/Users/[ \t]*\n((?:[ \t+|>]|\xE2\x94\x82|\xE2\x8E\xBF)*)(?!<user>)([A-Za-z0-9._-]+)(?=/)}g) { my $l = (substr($_, 0, $-[0]) =~ tr/\n//) + 1; print "$l: /Users/ wrapped onto the next line\n" }' || true)
  [[ -n "$wrapped" ]] && hits="${hits:+$hits$'\n'}$wrapped"
  # The account name of this machine (not on CI, where it is just "runner") and any
  # SANITIZE_EXTRA_NAMES, if still present under any spelling.
  for n in $names; do
    (( ${#n} >= 3 )) || continue
    named=$(show "$f" | grep -nwF -- "$n" | cut -d: -f1 | sed "s/\$/: account name '$n' present/" || true)
    [[ -n "$named" ]] && hits="${hits:+$hits$'\n'}$named"
  done
  if [[ -n "$hits" ]]; then
    echo "ai-sessions: $f still contains sensitive data (run make sanitize-sessions):" >&2
    printf '%s\n' "$hits" | sed -E 's/(sk-ant-.{4}).*/\1…/' | sed 's/^/  line /' >&2
    status=1
  fi
done <<< "$files"
exit $status
