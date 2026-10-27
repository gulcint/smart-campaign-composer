SHELL := /bin/bash
GOBIN := $(shell go env GOPATH 2>/dev/null)/bin
export PATH := $(GOBIN):$(PATH)

.DEFAULT_GOAL := help

TEST_COMPOSE := docker compose -f compose.test.yml

.PHONY: help hooks test test-hooks test-unit test-integration test-web db-roundtrip seed-manifest lint check-tool-schemas secrets sanitize-sessions check-sessions web-lint web-build check-contract-copies api-lint api-breaking api-routes api-types api-types-check

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

hooks: ## Install git hooks and the commit message template
	git config commit.template .gitmessage
	lefthook install

test: ## Run every test in containers, without ANTHROPIC_API_KEY (needs only Docker + make)
	@status=0; $(TEST_COMPOSE) run --rm --build test || status=$$?; \
	$(TEST_COMPOSE) run --rm --build web-test || status=$$?; \
	$(TEST_COMPOSE) down -v --remove-orphans; exit $$status

test-web: ## Vitest (web/tests) in the Node test container
	@status=0; $(TEST_COMPOSE) run --rm --build web-test || status=$$?; \
	$(TEST_COMPOSE) down -v --remove-orphans; exit $$status

test-hooks: ## Behaviour tests for the agent guards and scripts/worktree-remove.sh
	scripts/tests/guard-destructive-git.sh

test-unit: ## Go unit tests on the host, no DB, mock LLM
	@test -f go.mod || { echo "make test-unit: no go.mod yet; Go code starts in P2"; exit 1; }
	env -u ANTHROPIC_API_KEY LLM_PROVIDER=mock go test -race -short -count=1 -shuffle=on ./...

test-integration: ## Go tests of internal/ and cmd/ against the containerised Postgres (no E2E packages)
	@status=0; $(TEST_COMPOSE) run --rm --build test scripts/test-container.sh ./internal/... ./cmd/... || status=$$?; \
	$(TEST_COMPOSE) down -v --remove-orphans; exit $$status

db-roundtrip: ## Migrations up -> down -> up on a throwaway Postgres; schema must be identical
	scripts/db-roundtrip.sh

seed-manifest: ## Regenerate testdata/seed/manifest.json (seed 42, 5000 users, fixed ref time)
	go run ./cmd/seed --skip-db --seed=42 --users=5000 --ref-time=2026-01-15T12:00:00Z

lint: ## golangci-lint over the whole module
	@test -f go.mod || { echo "make lint: no go.mod yet; Go code starts in P2"; exit 1; }
	golangci-lint run ./...
	@$(MAKE) --no-print-directory check-tool-schemas
	@$(MAKE) --no-print-directory check-contract-copies

check-tool-schemas: ## The embedded LLM tool schemas must equal the contract copies
	@for f in segment-rule content; do \
	  cmp -s docs/contracts/schemas/llm/$$f.tool.json internal/llm/schemas/$$f.tool.json \
	  || { echo "internal/llm/schemas/$$f.tool.json differs from docs/contracts/schemas/llm/$$f.tool.json"; exit 1; }; \
	done

# Files embedded as httpapi.OpenAPI (go:embed cannot reach docs/); edit docs/contracts/, then copy.
CONTRACT_COPIES := openapi.yaml schemas/segment-rule.schema.json schemas/content.schema.json

check-contract-copies: ## internal/httpapi/openapi/ must be byte-identical to docs/contracts/
	@for f in $(CONTRACT_COPIES); do \
	  cmp -s docs/contracts/$$f internal/httpapi/openapi/$$f \
	  || { echo "internal/httpapi/openapi/$$f differs from docs/contracts/$$f (copy it over)"; exit 1; }; \
	done

api-lint: ## Redocly lint of the OpenAPI contract (pinned Docker image, offline); errors and warnings fail
	scripts/api-lint.sh

api-breaking: ## oasdiff breaking changes of the contract vs API_BASE (default origin/main); skips if absent there
	scripts/api-breaking.sh

api-routes: ## httpapi.Routes() <-> openapi.yaml paths x methods (test/contract, provided by the test writer)
	@if [ -d test/contract ]; then go test -count=1 ./test/contract/...; \
	else echo "api-routes: test/contract not present yet, skipping"; fi

web/node_modules: web/package.json web/package-lock.json
	cd web && npm ci
	@touch web/node_modules

web-lint: web/node_modules api-types-check ## ESLint (vue/no-v-html: error) + Prettier + vue-tsc + API types freshness for web/
	cd web && npm run lint && npm run typecheck

api-types: web/node_modules ## Regenerate web/src/api/schema.gen.ts from docs/contracts/openapi.yaml
	cd web && npm run api-types

api-types-check: web/node_modules ## Fail if web/src/api/schema.gen.ts is stale against docs/contracts/openapi.yaml
	@tmp=$$(mktemp); trap 'rm -f "$$tmp"' EXIT; \
	(cd web && node scripts/api-types.mjs "$$tmp") && \
	diff -u web/src/api/schema.gen.ts "$$tmp" \
	|| { echo "web/src/api/schema.gen.ts is stale: run 'make api-types' and commit it"; exit 1; }

web-build: web/node_modules ## Type-check and build the web bundle into web/dist
	cd web && npm run build

secrets: ## Scan the full git history for secrets
	gitleaks git --redact --no-banner -c .gitleaks.toml

sanitize-sessions: ## Mask keys, home paths and emails in ai-sessions/ exports
	scripts/sanitize-sessions.sh

check-sessions: ## Verify ai-sessions/ has no sensitive leftovers
	scripts/hooks/check-ai-sessions.sh --all
