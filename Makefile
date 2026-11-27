SHELL := /bin/bash
.DEFAULT_GOAL := help

# Local overrides (git-ignored). See .env.example. Every key in .env is
# exported to commands such as `make api`.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

DATABASE_URL ?= postgres://slotd:slotd@localhost:5432/slotd?sslmode=disable
export DATABASE_URL

# Pinned tool versions. Tools run via `go run pkg@version` so they stay out of go.mod.
GOOSE_VERSION       := v3.30.2
GOVULNCHECK_VERSION := v1.10.0
GOOSE               := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
GOVULNCHECK         := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
# golangci-lint is a prebuilt binary (upstream advises against `go run`); keep in sync with CI.
GOLANGCI_LINT       ?= golangci-lint

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: up
up: ## Start local dependencies (Postgres, NATS, Jaeger, Prometheus, Grafana)
	docker compose up -d --wait

.PHONY: down
down: ## Stop local dependencies (keeps volumes)
	docker compose down

.PHONY: reset
reset: ## Stop local dependencies and delete their volumes
	docker compose down -v

.PHONY: migrate
migrate: ## Apply database migrations
	$(GOOSE) -dir migrations postgres "$(DATABASE_URL)" up

.PHONY: api
api: ## Run the API locally (needs `make up migrate`)
	go run ./cmd/api

.PHONY: gen
gen: ## Regenerate sqlc queries and OpenAPI types (go:generate, pinned versions)
	go generate ./...

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

.PHONY: test-int
test-int: ## Run unit + integration tests (needs Docker for testcontainers)
	go test -race -count=1 -tags=integration ./...

.PHONY: bench
bench: ## Run benchmarks
	go test -run='^$$' -bench=. -benchmem ./...

.PHONY: vuln
vuln: ## Scan dependencies and stdlib for known vulnerabilities
	$(GOVULNCHECK) ./...

.PHONY: load
load: ## Run k6 load tests (Phase 5)
	@echo "load: k6 scenarios are added in Phase 5 (loadtest/)" >&2; exit 1

.PHONY: demo
demo: ## Run the end-to-end demo (Phase 6)
	@echo "demo: the scripted demo is added in Phase 6" >&2; exit 1
