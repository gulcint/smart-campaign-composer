#!/usr/bin/env bash
# Migration hygiene (docs/PLAN.md §2.1):
#   naming NNNN_name.(up|down).sql, up/down pairs, unique numbers,
#   immutability of migrations already on origin/main,
#   DROP TABLE/COLUMN only with an explicit "-- allow-destructive" marker.
# Usage: check-migrations.sh                 # staged changes (pre-commit)
#        check-migrations.sh --range <base>  # changes in <base>..HEAD (CI)
set -euo pipefail

if [[ "${1:-}" == "--range" ]]; then
  base="$2"
  name_status() { git diff --name-status "$@" "$base" HEAD -- migrations/; }
  changed()     { git diff --name-only --diff-filter=AM "$base" HEAD -- migrations/; }
  tracked()     { git ls-tree -r --name-only HEAD -- migrations/; }
  content()     { git show "HEAD:$1"; }
else
  name_status() { git diff --cached --name-status "$@" -- migrations/; }
  changed()     { git diff --cached --name-only --diff-filter=AM -- migrations/; }
  tracked()     { git ls-files -- migrations/; }
  content()     { git show ":$1"; }
fi

name_re='^migrations/[0-9]{4}_[a-z0-9_]+\.(up|down)\.sql$'
status=0
err() { echo "migrations: $*" >&2; status=1; }

# Immutability: anything already on origin/main may not be modified, renamed or deleted.
if git rev-parse --verify --quiet origin/main >/dev/null; then
  while IFS=$'\t' read -r st path _; do
    [[ -z "${st:-}" ]] && continue
    if git cat-file -e "origin/main:$path" 2>/dev/null; then
      err "$path is already on main and must not change ($st); add a new migration instead"
    fi
  done < <(name_status --diff-filter=MDR)
fi

staged=$(changed)
[[ -z "$staged" ]] && exit $status

# Full set of migration files as they will be after this change.
all=$( { tracked; printf '%s\n' "$staged"; } | sort -u | grep -v '^$' || true)

while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  # db-schema.md §2: migrations/embed.go embeds the SQL files for cmd/migrate.
  [[ "$f" == "migrations/embed.go" ]] && continue
  [[ "$f" =~ $name_re ]] || { err "$f: expected migrations/NNNN_name.up.sql or .down.sql"; continue; }

  stem="${f%.*.sql}"
  if [[ "$f" == *.up.sql ]]; then pair="$stem.down.sql"; else pair="$stem.up.sql"; fi
  printf '%s\n' "$all" | grep -qxF "$pair" || err "$f has no matching $pair"

  if content "$f" | grep -Eiq 'DROP[[:space:]]+(TABLE|COLUMN)' \
     && ! content "$f" | grep -q -- '-- allow-destructive'; then
    err "$f contains DROP TABLE/COLUMN without '-- allow-destructive'"
  fi
done <<< "$staged"

# Each number must belong to exactly one migration name.
dupes=$(printf '%s\n' "$all" | grep -E "$name_re" \
  | sed -E 's#^migrations/([0-9]{4})_(.*)\.(up|down)\.sql$#\1 \2#' | sort -u \
  | awk '{print $1}' | uniq -d)
[[ -n "$dupes" ]] && err "migration number(s) used by more than one name: $dupes"

exit $status
