# ============================================================================
# ADATRACK backend — developer entry points (PRD §14.3)
# ============================================================================
# `make help` lists everything. All targets are variant-aware via
# COMPOSE_VARIANT=local|coolify (§7).
# ============================================================================
SHELL := /bin/bash
ROOT  := $(shell cd . && pwd)
VARIANT ?= local
MODULES := internal services/ingestion-tcp services/worker-live services/worker-persistence services/foundation-check tools/e2e

.DEFAULT_GOAL := help
.PHONY: help up down ps logs build test test-race fmt vet reset-db migrate provision-tenant seed services-up services-down e2e clean

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

up: ## Start infra (compose) — VARIANT=local|coolify
	@scripts/compose-up.sh $(VARIANT) up -d

down: ## Stop infra (compose)
	@scripts/compose-up.sh $(VARIANT) down

ps: ## Show infra container status + health
	@scripts/compose-up.sh $(VARIANT) ps

logs: ## Tail infra logs
	@scripts/compose-up.sh $(VARIANT) logs -f --tail=100

migrate: ## Apply DB bootstrap + migrations + seed (+ ledger verify)
	@scripts/migrate.sh $(VARIANT)

reset-db: ## Drop ADATRACK schemas and re-provision (DESTRUCTIVE)
	@scripts/reset-db.sh

provision-tenant: ## Provision a tenant: make provision-tenant CODE=ACME NAME="PT Acme"
	@test -n "$(CODE)" || { echo "CODE is required, e.g. make provision-tenant CODE=ACME"; exit 1; }
	@COMPOSE_VARIANT=$(VARIANT) scripts/provision-tenant.sh "$(CODE)" "$(NAME)" "$(BUSINESS_TYPE)"

build: ## Build every Go module
	@set -e; for m in $(MODULES); do echo "build: $$m"; (cd $$m && go build ./...); done
	@mkdir -p bin; for s in ingestion-tcp worker-live worker-persistence foundation-check; do (cd services/$$s && go build -o $(ROOT)/bin/$$s .); done
	@echo "build: ok (bin/)"

test: ## Run unit + integration tests (all modules)
	@scripts/test.sh

test-race: ## Run tests with the race detector
	@scripts/test.sh --race

fmt: ## gofmt every module
	@set -e; for m in $(MODULES); do (cd $$m && gofmt -l -w .); done

vet: ## go vet every module
	@set -e; for m in $(MODULES); do (cd $$m && go vet ./...); done

services-up: ## Build + start pipeline services on the dev host
	@scripts/start-services.sh up

services-down: ## Stop host-run pipeline services
	@scripts/start-services.sh down

e2e: ## End-to-end pipeline test (device frame → NATS → Redis + PostgreSQL)
	@scripts/e2e-pipeline.sh

clean: ## Remove build artifacts
	@rm -rf bin logs/*.log logs/pids monitoring/targets/services.json
	@echo "clean: ok"