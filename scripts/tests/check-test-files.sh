#!/usr/bin/env bash
# Behaviour tests for scripts/hooks/check-test-files.sh. Run: make test-hooks
set -uo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
check="$root/scripts/hooks/check-test-files.sh"
fails=0

expect() { # <block|allow> <path>
  local got=allow
  "$check" -- "$2" >/dev/null 2>&1 || got=block
  if [[ "$got" == "$1" ]]; then echo "PASS: $1  $2"; else echo "FAIL: expected $1, got $got: $2"; fails=$((fails + 1)); fi
}

for f in internal/httpapi/content_round2_test.go test/e2e/content_round2_test.go \
         internal/campaign/service_round3_integration_test.go internal/content/fix_test.go \
         internal/content/validate_fix_test.go web/tests/editor.review.spec.ts web/tests/round2.spec.ts \
         internal/audience/review_test.go; do
  expect block "$f"
done
for f in internal/content/validate_test.go internal/campaign/approve_integration_test.go \
         internal/content/prefix_test.go test/e2e/background_test.go web/tests/editor.spec.ts \
         docs/review-round2.md internal/content/round2.go; do
  expect allow "$f"
done

echo
if (( fails )); then echo "$fails test(s) failed"; exit 1; fi
echo "all test-file-name tests passed"
