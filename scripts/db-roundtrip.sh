#!/usr/bin/env bash
# Migration round trip (docs/PLAN.md §2.2, db-schema.md §2): on a throwaway Postgres,
# apply all migrations, revert all, apply again, and require the two schema dumps to be
# identical. Called by `make db-roundtrip` and the pre-push hook. Without Docker it warns
# and exits 0 locally (CI is the enforcement).
set -euo pipefail

image="postgres:16-bookworm@sha256:d81f7b51697014367236d07ec58b54153fc28f828b72bf247fe5c7487520bcb0"
root="$(cd "$(dirname "$0")/.." && pwd)"

if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "db-roundtrip: Docker is not available; skipping (CI runs it)" >&2
  exit 0
fi

name="scc-db-roundtrip-$$"
tmp="$(mktemp -d)"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT

docker run -d --name "$name" \
  -e POSTGRES_PASSWORD=roundtrip -e SCC_MIGRATOR_PASSWORD=roundtrip -e SCC_APP_PASSWORD=roundtrip \
  -e SCC_DATABASES=scc \
  -v "$root/docker/postgres/initdb:/docker-entrypoint-initdb.d:ro" \
  -p 127.0.0.1::5432 "$image" >/dev/null

# The entrypoint restarts the server after running init scripts; wait for the final one.
for _ in $(seq 1 60); do
  if docker exec "$name" pg_isready -U postgres -d scc -h 127.0.0.1 >/dev/null 2>&1 \
     && docker exec "$name" psql -U postgres -d scc -h 127.0.0.1 -tAc 'SELECT 1' >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

port="$(docker port "$name" 5432/tcp | head -n1 | sed 's/.*://')"
export MIGRATE_DATABASE_URL="postgres://scc_migrator:roundtrip@127.0.0.1:${port}/scc?sslmode=disable"
# pg_dump wraps its output in \restrict/\unrestrict lines with a random token; drop them.
dump() {
  docker exec "$name" pg_dump -U postgres -d scc --schema-only --no-owner --no-privileges -h 127.0.0.1 \
    | grep -Ev '^\\(un)?restrict '
}

cd "$root"
go build -o "$tmp/migrate" ./cmd/migrate
"$tmp/migrate" up
dump > "$tmp/first.sql"
"$tmp/migrate" down
leftover="$(docker exec "$name" psql -U postgres -d scc -h 127.0.0.1 -tAc \
  "SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
   WHERE n.nspname = 'public' AND c.relname NOT LIKE 'schema\\_migrations%'")"
if [[ "$leftover" != "0" ]]; then
  echo "db-roundtrip: $leftover relations left in public after down" >&2
  exit 1
fi
"$tmp/migrate" up
dump > "$tmp/second.sql"
if ! diff -u "$tmp/first.sql" "$tmp/second.sql"; then
  echo "db-roundtrip: schema differs after up -> down -> up" >&2
  exit 1
fi
echo "db-roundtrip: up -> down -> up OK, schema identical"
