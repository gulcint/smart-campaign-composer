#!/usr/bin/env bash
# Validates a commit message (or a PR title) against docs/PLAN.md §1.4.
# Usage: check-commit-msg.sh <message-file>
#        check-commit-msg.sh --subject-only <file>   # PR titles: no Refs: line expected
set -euo pipefail

subject_only=0
if [[ "$1" == "--subject-only" ]]; then subject_only=1; shift; fi

msg_file="$1"
# Drop comment lines git leaves in the editor buffer.
msg=$(grep -v '^#' "$msg_file" || true)
subject=$(printf '%s\n' "$msg" | sed -n '1p')

case "$subject" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*) exit 0 ;;
esac

types='feat|fix|test|docs|chore|ci|refactor|sec'
scopes='audience|content|approval|delivery|tracking|llm|seed|db|ui|infra|ci|docs'
pattern="^($types)(\(($scopes)\))?!?: [^ ].*$"

fail() {
  echo "invalid commit message: $1" >&2
  echo "  subject: $subject" >&2
  echo "  expected: <type>(<scope>): <summary>   see .gitmessage" >&2
  exit 1
}

[[ "$subject" =~ $pattern ]] || fail "subject does not match Conventional Commits with allowed types/scopes"
(( ${#subject} <= 72 )) || fail "subject is ${#subject} chars, max 72"
(( subject_only )) && exit 0

type="${subject%%[(:!]*}"
case "$type" in
  feat|fix|test)
    printf '%s\n' "$msg" | grep -Eq '^Refs: REQ-[A-Z]+-[0-9]{2}(, REQ-[A-Z]+-[0-9]{2})*$' \
      || fail "'$type' commits need a 'Refs: REQ-XXX-NN' line"
    ;;
esac
