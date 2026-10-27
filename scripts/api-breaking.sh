#!/usr/bin/env bash
# api-breaking gate (contracts README §11): oasdiff breaking-change check of the working-tree
# contract (docs/contracts/openapi.yaml + schemas/) against the same files at a base revision.
# Usage: scripts/api-breaking.sh [base-ref]   (default: $API_BASE or origin/main)
# Skips (exit 0) when the base revision has no openapi.yaml. Intentional breaking changes are
# justified in the PR and DECISIONS.md; CI lets them through with the `api-breaking-ok` label.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
base="${1:-${API_BASE:-origin/main}}"
# oasdiff v1.33.0 (multi-arch index digest).
image="tufin/oasdiff:v1.33.0@sha256:6263a96dd2ef0726c54e21fea9b8e1607eac4841add0079324b424c1f52b819c"

if ! git -C "$root" cat-file -e "$base:docs/contracts/openapi.yaml" 2>/dev/null; then
  echo "api-breaking: $base has no docs/contracts/openapi.yaml; nothing to compare, skipping"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
# The spec is multi-file (relative $refs), so the whole contracts tree of the base is extracted.
git -C "$root" archive "$base" docs/contracts | tar -x -C "$tmp"

docker run --rm --network none \
  -v "$tmp/docs/contracts:/base:ro" -v "$root/docs/contracts:/head:ro" "$image" \
  breaking /base/openapi.yaml /head/openapi.yaml --fail-on ERR
echo "api-breaking: no breaking change against $base"
