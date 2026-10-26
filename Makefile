SHELL := /bin/bash
GOBIN := $(shell go env GOPATH 2>/dev/null)/bin
export PATH := $(GOBIN):$(PATH)

.DEFAULT_GOAL := help

TEST_COMPOSE := docker compose -f compose.test.yml

.PHONY: help hooks test test-unit test-integration db-roundtrip seed-manifest lint check-tool-schemas secrets sanitize-sessions check-sessions

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

hooks: ## Install git hooks and the commit message template
	git config commit.template .gitmessage
	lefthook install

test: ## Run every test in containers, without ANTHROPIC_API_KEY (needs only Docker + make)
	@status=0; $(TEST_COMPOSE) run --rm --build test || status=$$?; \
	$(TEST_COMPOSE) down -v --remove-orphans; exit $$status

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

check-tool-schemas: ## The embedded LLM tool schemas must equal the contract copies
	@for f in segment-rule content; do \
	  cmp -s docs/contracts/schemas/llm/$$f.tool.json internal/llm/schemas/$$f.tool.json \
	  || { echo "internal/llm/schemas/$$f.tool.json differs from docs/contracts/schemas/llm/$$f.tool.json"; exit 1; }; \
	done

secrets: ## Scan the full git history for secrets
	gitleaks git --redact --no-banner -c .gitleaks.toml

sanitize-sessions: ## Mask keys, home paths and emails in ai-sessions/ exports
	scripts/sanitize-sessions.sh

check-sessions: ## Verify ai-sessions/ has no sensitive leftovers
	scripts/hooks/check-ai-sessions.sh --all
