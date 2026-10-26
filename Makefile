SHELL := /bin/bash
GOBIN := $(shell go env GOPATH 2>/dev/null)/bin
export PATH := $(GOBIN):$(PATH)

.DEFAULT_GOAL := help

.PHONY: help hooks test test-unit lint secrets sanitize-sessions check-sessions

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

hooks: ## Install git hooks and the commit message template
	git config commit.template .gitmessage
	lefthook install

test: ## Run every test in containers, without ANTHROPIC_API_KEY (needs only Docker + make)
	@test -f compose.test.yml || { echo "make test: compose.test.yml does not exist yet; it is built in P2 (docs/PLAN.md §4.3)"; exit 1; }
	docker compose -f compose.test.yml run --rm --build test

test-unit: ## Go unit tests on the host, no DB, mock LLM
	@test -f go.mod || { echo "make test-unit: no go.mod yet; Go code starts in P2"; exit 1; }
	env -u ANTHROPIC_API_KEY LLM_PROVIDER=mock go test -race -short -count=1 -shuffle=on ./...

lint: ## golangci-lint over the whole module
	@test -f go.mod || { echo "make lint: no go.mod yet; Go code starts in P2"; exit 1; }
	golangci-lint run ./...

secrets: ## Scan the full git history for secrets
	gitleaks git --redact --no-banner -c .gitleaks.toml

sanitize-sessions: ## Mask keys, home paths and emails in ai-sessions/ exports
	scripts/sanitize-sessions.sh

check-sessions: ## Verify ai-sessions/ has no sensitive leftovers
	scripts/hooks/check-ai-sessions.sh --all
