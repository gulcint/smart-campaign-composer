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
email_re='[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
# Allowed: placeholders, RFC 2606 domains, Anthropic's commit trailer address.
allow_re='/Users/<user>|@example\.(com|test|org|net)|noreply@anthropic\.com|\[REDACTED'

status=0
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  hits=$(show "$f" | grep -nEo "$key_re|$home_re|$email_re" | grep -Ev "$allow_re" || true)
  if [[ -n "$hits" ]]; then
    echo "ai-sessions: $f still contains sensitive data (run make sanitize-sessions):" >&2
    printf '%s\n' "$hits" | sed -E 's/(sk-ant-.{4}).*/\1…/' | sed 's/^/  line /' >&2
    status=1
  fi
done <<< "$files"
exit $status
