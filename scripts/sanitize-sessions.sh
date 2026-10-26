#!/usr/bin/env bash
# Masks API keys, home-directory paths and personal emails in session exports, in place.
# Usage: sanitize-sessions.sh [file...]    # default: every file under ai-sessions/ except INDEX.md
set -euo pipefail

if (( $# == 0 )); then
  set -- $(find ai-sessions -type f ! -name INDEX.md)
fi
(( $# == 0 )) && { echo "nothing to sanitize"; exit 0; }

for f in "$@"; do
  perl -pi -e '
    s/sk-ant-[A-Za-z0-9_-]{10,}/[REDACTED_API_KEY]/g;
    s#/Users/[A-Za-z0-9._-]+#/Users/<user>#g;
    s/\b[A-Za-z0-9._%+-]+@(?!example\.(?:com|test|org|net)\b)(?!anthropic\.com\b)[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/[REDACTED_EMAIL]/g;
  ' "$f"
  echo "sanitized $f"
done
echo "review the files manually before committing (docs/PLAN.md §7)"
