#!/usr/bin/env bash
# Masks API keys, home-directory paths and personal emails in session exports, in place.
# Usage: sanitize-sessions.sh [file...]    # default: every file under ai-sessions/ except INDEX.md
# Beyond the plain patterns it handles what line wrapping and path slugs do to a home path:
#   "/Users/" at the end of a line with the account name at the start of the next
#   Claude Code project slugs such as "-Users-<name>-Desktop-..."
#   the account name of the machine running this script ($USER) and SANITIZE_EXTRA_NAMES
#   (space-separated), wherever they appear as a whole word
#   SANITIZE_EXTRA_PHRASES (comma-separated, may contain spaces): full names such as "First Last",
#   masked case-insensitively with any whitespace between the words
set -euo pipefail
export SANITIZE_NAMES="${USER:-} ${SANITIZE_EXTRA_NAMES:-}"
export SANITIZE_PHRASES="${SANITIZE_EXTRA_PHRASES:-}"

if (( $# == 0 )); then
  set -- $(find ai-sessions -type f ! -name INDEX.md)
fi
(( $# == 0 )) && { echo "nothing to sanitize"; exit 0; }

for f in "$@"; do
  perl -0777 -pi -e '
    s/sk-ant-[A-Za-z0-9_-]{10,}/[REDACTED_API_KEY]/g;
    # A line-wrapped home path: "/Users/" ends a line, the account name starts the next
    # (optionally behind list/box-drawing/diff prefixes, matched as raw UTF-8 bytes).
    s{(/Users/[ \t]*\n(?:[ \t+|>]|\xE2\x94\x82|\xE2\x8E\xBF)*)(?!<user>)[A-Za-z0-9._-]+(?=/)}{$1<user>}g;
    s#/Users/(?!<user>)[A-Za-z0-9._-]+#/Users/<user>#g;
    s#-Users-(?!<user>)[A-Za-z0-9._]+-#-Users-<user>-#g;
    for my $n (grep { length($_) >= 3 } split " ", $ENV{SANITIZE_NAMES}) {
      s/(?<![A-Za-z0-9])\Q$n\E(?![A-Za-z0-9])/<user>/g;
    }
    for my $p (grep { length($_) >= 3 } map { s/^\s+|\s+$//gr } split /,/, $ENV{SANITIZE_PHRASES}) {
      my $re = join q{\s+}, map { quotemeta } split " ", $p;
      s/(?<![A-Za-z0-9])$re(?![A-Za-z0-9])/<user>/gi;
    }
    # Same allowlist as check-ai-sessions.sh: RFC 2606 domains and noreply@anthropic.com.
    # Copy $1 first: the inner match below resets it.
    s{\b([A-Za-z0-9._%+-]+\@[A-Za-z0-9.-]+\.[A-Za-z]{2,})\b}{
      my $e = $1;
      $e =~ /^noreply\@anthropic\.com$|\@example\.(?:com|test|org|net)$/ ? $e : "[REDACTED_EMAIL]"
    }ge;
  ' "$f"
  echo "sanitized $f"
done
echo "review the files manually before committing (docs/PLAN.md §7)"
