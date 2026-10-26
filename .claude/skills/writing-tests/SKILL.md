---
name: writing-tests
description: Rules for writing Smart Campaign Composer tests from the case document and contracts only — REQ traceability, Go table-driven style, determinism (clock, randomness, network, DB, LLM), test layers, make test contract and the test writer's git workflow. Use when writing, reviewing or planning any test in this repo.
---

# Writing tests for Smart Campaign Composer

Tests measure whether the implementation satisfies the case. They are derived from the
**case document and the contracts**, never from the implementation (docs/PLAN.md §3.1, §4).

## 1. Sources you may use — and nothing else

| Source | Path | Use it for |
|---|---|---|
| Case document (gitignored) | `<main repo>/Smart Campaign Composer Case.html` | What the product must do; the final word |
| Requirements catalog | `docs/requirements.md` | REQ IDs, one per behavior |
| Contracts | `docs/contracts/**` | The only surfaces tests may touch |
| Plan | `docs/PLAN.md` §0.4, §0.5, §4, §5 | Retry policy, stream protocol, test rules, seed manifest |
| Existing tests | `**/*_test.go`, `test/**`, `testdata/**`, `web/tests/**` | Style and shared helpers |

The contract surfaces are: HTTP API (OpenAPI), SSE events (PLAN §0.5), Go interface
signatures (`docs/contracts/go-interfaces.md`), observable DB results, and the UI contract
(`docs/contracts/ui.md`: components, props, `data-testid`s).

**If a contract you need does not exist or is ambiguous, stop.** Do not invent a function
name, field, endpoint or error. Write down the question (what the doc says, what is missing,
which REQ it blocks) and hand it back to the owner. A test against a guessed signature
measures your guess, not the system.

**If a test seems wrong after the implementation exists, the test is not changed to match
the code.** The owner decides; the architect fixes `docs/`; then the test follows `docs/`.

## 2. What the case document requires of tests

Quoted requirements every suite must cover (IDs from `docs/requirements.md`):

- AI output is structured JSON and validated in the backend (REQ-AI-01).
- **Tests run without a real API key, with a mock LLM** (REQ-AI-02).
- Segment rule is validated; LLM output is never executed as SQL (REQ-AUD-01/02).
- No message on a channel without consent (REQ-AUD-04).
- SMS ≤ 160 chars, email subject ≤ 80 chars; `{{first_name}}`-style variables (REQ-CNT-01..03).
- Async queue, ≤ 100 msg/s per channel; same person never gets the same message twice;
  a retry strategy (REQ-SND-01..04).
- Live status, metrics, health endpoints (REQ-TRK-01..03).
- Mock provider sometimes returns errors, delays and 429 so retry and rate limiting are
  really tested (REQ-MOCK-01/02).
- `make test` runs the tests (REQ-DLV-02).

## 3. Traceability

- Every test function starts with a comment naming its requirement(s): `// REQ-CNT-01`.
- One behavior per subtest; a REQ may have many tests, a test should rarely cover many REQs.
- When you finish, report the REQ → test mapping and any REQ you could not cover and why.

## 4. Layers and where tests live

| Layer | Location | Touches | Runs in |
|---|---|---|---|
| Unit | `internal/<pkg>/*_test.go`, `package <pkg>_test` | Contracted Go functions/interfaces only | `make test-unit`, `make test` |
| LLM (mock) | same, fixtures in `testdata/llm/` | `llm.Mock` scenarios, SSE event order | both |
| Integration (DB) | `*_test.go` guarded by `testing.Short()` | Postgres from `compose.test.yml`, seed manifest | `make test` |
| Delivery | `test/delivery/` | worker + scripted `httptest` provider, and the real `cmd/mockprovider` (R10) | `make test` |
| API E2E | `test/e2e/` | HTTP + SSE per OpenAPI, key-less | `make test` |
| Frontend | `web/tests/**/*.spec.ts` (Vitest + Vue Test Utils) | Only props and `data-testid`s from `docs/contracts/ui.md` | `make test` |

Integration tests skip themselves under `-short` (that is how `make test-unit` excludes them);
this is layering, not a disabled test. Any other `t.Skip`, `.only` or `.skip` is forbidden.

## 5. Go style

- Names: `Test<Unit>_<Behavior>`; subtest names are sentences: `"161 chars is rejected"`.
- Black box: `package x_test`. Never test unexported identifiers.
- Table-driven by default:

```go
// REQ-CNT-01
func TestSMSContent_LengthLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr error
	}{
		{name: "160 chars is accepted", body: strings.Repeat("a", 160)},
		{name: "161 chars is rejected", body: strings.Repeat("a", 161), wantErr: content.ErrSMSTooLong},
		{name: "Turkish letters count as one char each", body: strings.Repeat("ş", 160)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := content.ValidateSMS(tc.body) // signature and sentinel come from docs/contracts
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ValidateSMS() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
```

(The example's `content.ValidateSMS` / `ErrSMSTooLong` are illustrative; use the names the
contract actually defines.)

- Compare errors with `errors.Is` against contracted sentinels — never by message text.
- Compare structs with `github.com/google/go-cmp/cmp` (`cmp.Diff`); no assertion frameworks.
- `t.Helper()` in every helper; `t.Parallel()` unless the test shares state.
- Boundary values always: limit, limit±1, empty, Unicode (ğüşİı), the 29/30/31-day edges.

## 6. Determinism — non-negotiable

| Source | Rule |
|---|---|
| Time | Never `time.Now()`. Inject the contracted `Clock`; use a fake clock at a fixed reference (`2026-01-15T12:00:00Z`). **No `time.Sleep`**: advance the fake clock or wait on a channel with a timeout. |
| Randomness | No global `math/rand`. Pass a seeded `*rand.Rand` (jitter, provider fault injection, seed data). |
| Network | `httptest.Server` only. Never reach a real host; the Anthropic API is unreachable in tests by design. |
| Database | No DB in unit tests. Integration tests isolate per test (transaction rollback or own schema) and must pass under `-shuffle=on`. |
| LLM | `llm.Mock` with fixtures from `testdata/llm/`; stream chunks in a fixed order. |
| Concurrency | Must pass with `-race`; check goroutine leaks with `goleak` where goroutines start. |
| Seed data | Use counts from `testdata/seed/manifest.json` (read-only for you: the seed code generates it and the implementer commits it) with the fixed `--seed`/`--ref-time`; never hard-code a count you computed yourself. |

A flaky test is a bug in the test or the system — fix it, never quarantine it.

## 7. Scenarios to cover per area (from PLAN §4.4)

- **Audience:** unknown field/operator, excessive nesting and SQL-injection values are rejected
  or stay parameters; consent excluded per channel; count is per channel after consent.
- **Content:** 160/161, 80/81; limit checked after rendering with the longest `first_name`;
  unknown variable rejected; empty `first_name` fallback.
- **LLM:** valid JSON accepted; broken JSON and schema violations rejected (one repair attempt,
  then a clear error); prompt injection in the brief cannot escape the schema; stream cut
  mid-way; SSE order `delta…` → `validated` | `invalid`; approval refused before `validated`.
- **Approval:** edited SMS of 161 → 422; edited unknown variable rejected; editing after
  approval drops back to `content_ready` and send returns 409; API calls that bypass the UI
  are validated too.
- **Delivery:** 429 honors `Retry-After` and has its own budget; 5xx backoff; 4xx no retry;
  "accepted but response lost" does not double-send (DB unique + `Idempotency-Key`);
  ≤ 100/s per channel **with several workers** and retries counted.
- **Tracking:** status reaches `completed`; `/healthz`, `/readyz`, `/metrics` respond.
- **Key-less run:** with no key and `LLM_PROVIDER=auto` the mock is selected; `real` without
  a key fails at startup; logs never contain the key.

## 8. Fixtures

- `testdata/` next to the package, or `testdata/llm/` for shared LLM responses.
- Golden files are updated only by the test writer with `-update`, and the diff is reviewed.
- Fixture data is synthetic: `example.com` emails, fictional phone block, no real names of
  real people.

## 9. Git workflow for the test writer

1. Work only in the worktree created by `scripts/test-writer-worktree.sh <base> test/p<N>-<desc>`
   (implementation directories are not checked out there).
2. Commit with the template; `test` commits need `Refs:`:

```
test(content): cover SMS and subject length limits

Boundary, Unicode and post-render cases from the case document.

Refs: REQ-CNT-01, REQ-CNT-02
Phase: P4
```

3. Push the `test/*` branch and stop: the owner opens the PR into the `slice/*` branch.
   CI's role check fails a `test/*` branch that touches implementation paths.
4. Tests are expected to be red until the implementer's branch lands — that is the point.
