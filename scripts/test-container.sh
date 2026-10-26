#!/usr/bin/env bash
# Entry point of the `test` service in compose.test.yml (make test).
# Prepares the integration database, waits for the E2E API and runs the Go tests
# (unit + integration + E2E; integration tests skip themselves only under -short).
# Usage: scripts/test-container.sh [go test packages...]   (default ./...)
set -euo pipefail

if [[ -n "${ANTHROPIC_API_KEY+x}" ]]; then
  echo "test-container: ANTHROPIC_API_KEY must not be defined in the test environment" >&2
  exit 1
fi

wait_for() { # <description> <command...>
  local what="$1"; shift
  for _ in $(seq 1 90); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "test-container: timed out waiting for $what" >&2
  return 1
}

# Integration database: same schema and seed as the API database, owned by this run.
MIGRATE_DATABASE_URL="$TEST_MIGRATE_DATABASE_URL" wait_for "migrations on the test database" migrate up
seed --seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z --db-url="$TEST_DATABASE_URL" --manifest=- >/dev/null

wait_for "the API at $E2E_API_URL" curl -fsS "$E2E_API_URL/healthz"
wait_for "the mock provider at $E2E_MOCKPROVIDER_URL" curl -fsS "$E2E_MOCKPROVIDER_URL/healthz"

pkgs=("$@")
if [[ ${#pkgs[@]} -eq 0 ]]; then pkgs=(./...); fi
exec go test -race -count=1 -shuffle=on "${pkgs[@]}"
