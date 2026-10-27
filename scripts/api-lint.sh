#!/usr/bin/env bash
# api-lint gate (contracts README §11): Redocly lint of the multi-file OpenAPI contract with
# docs/contracts/redocly.yaml. Fails on any error AND on any warning.
# Runs the pinned Redocly CLI image without network access (no npx, no registry at run time).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# @redocly/cli 2.71.1 (official image, multi-arch index digest).
image="redocly/cli:2.71.1@sha256:2c41d8209d975813eda77315944ede46afc9603179f359215cd8cd61da85ab00"

out="$(mktemp)"
trap 'rm -f "$out"' EXIT
status=0
docker run --rm --network none -v "$root/docs/contracts:/spec:ro" -w /spec "$image" \
  lint --config redocly.yaml openapi.yaml >"$out" 2>&1 || status=$?
cat "$out"
if [[ $status -ne 0 ]]; then
  echo "api-lint: redocly reported errors" >&2
  exit 1
fi
# Redocly exits 0 on warnings; the gate does not accept them.
if grep -Eqi '[0-9]+ warnings?' "$out"; then
  echo "api-lint: redocly reported warnings" >&2
  exit 1
fi
