# ============================================================================
# ADATRACK backend — developer entry points (PRD §14.3)
# ============================================================================
# `make help` lists everything. All targets are variant-aware via
# COMPOSE_VARIANT=local|coolify (§7).
# ============================================================================
SHELL := /bin/bash
ROOT  := $(shell cd . && pwd)
VARIANT ?= local
MODULES := internal services/ingestion-tcp services/worker-live services/worker-persistence services/service-websocket services/api-vehicle services/service-media services/worker-alert services/foundation-check tools/e2e tools/e2ews tools/e2e-media tools/e2e-fuel tools/querybench

.DEFAULT_GOAL := help
.PHONY: help up down ps logs build test test-race fmt vet reset-db migrate provision-tenant seed services-up services-down e2e e2e-ws e2e-media e2e-fuel clean monitoring-up monitoring-down prom-targets b4-verify backup-db restore-db backup-redis retention-purge querybench cover

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

up: ## Start the system stack — infra + monitoring (compose) — VARIANT=local|coolify
	@scripts/gen-prom-targets.sh >/dev/null || true
	@scripts/compose-up.sh $(VARIANT) up -d

down: ## Stop the system stack — infra + monitoring (compose)
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
	@mkdir -p bin; for s in ingestion-tcp worker-live worker-persistence service-websocket service-media foundation-check; do (cd services/$$s && go build -o $(ROOT)/bin/$$s .); done
	@echo "build: ok (bin/)"

test: ## Run unit + integration tests (all modules)
	@scripts/test.sh

test-race: ## Run tests with the race detector
	@scripts/test.sh --race

cover: ## Coverage for every app service, incl. those the B4 gate misses
	@scripts/coverage-report.sh $(COVER_ARGS)

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

e2e-ws: ## End-to-end REST + WebSocket test (login → RBAC → live push, B2)
	@scripts/e2e-websocket.sh

e2e-fuel: ## End-to-end fuel sensor test (frame 0x0D → td_fuel_logs → alert → WS, B5a)
	@scripts/e2e-fuel.sh

e2e-media: ## End-to-end dashcam media test (HMAC → MinIO → katalog → WS → retensi, B5b)
	@scripts/e2e-media.sh

clean: ## Remove build artifacts
	@rm -rf bin logs/*.log logs/pids monitoring/targets/*.json
	@echo "clean: ok"

# --- B4: performance, monitoring, hardening, DR (PRD §10–§13, §16–§17) -------

# Monitoring is NOT a separate stack anymore: Prometheus/Alertmanager/Grafana and
# the exporters are defined in docker-compose.yml next to postgres/redis/nats, so
# a single `up`/`down` owns everything. These two targets are kept as the
# documented entry points (docs, b4-verify) and simply delegate to `up`/`down`
# after regenerating the Prometheus file_sd targets.
monitoring-up: ## Start the stack incl. monitoring (alias for `up`)
	@scripts/gen-prom-targets.sh >/dev/null
	@scripts/compose-up.sh $(VARIANT) up -d
	@echo "monitoring-up: Prometheus :$${HOST_PROM_PORT:-9095} — Alertmanager :$${HOST_ALERTMANAGER_PORT:-9093} — Grafana :$${HOST_GRAFANA_PORT:-3001} (dashboard uid adatrack-core)"

monitoring-down: ## Stop the stack, monitoring included (alias for `down`)
	@scripts/compose-up.sh $(VARIANT) down

prom-targets: ## Regenerate Prometheus file_sd targets from the service ports
	@scripts/gen-prom-targets.sh

# --- HA overlay & drill (PRD §13) — varian LOCAL ---------------------------
ha-up: ## Start the HA overlay: PG standby + Redis replica (VARIANT=local)
	@docker compose -f docker-compose.local.yml -f deployments/docker-compose.ha.yml --env-file .env.$(VARIANT) up -d postgres-replica redis-replica

ha-down: ## Stop the HA overlay (primary stack keeps running)
	@docker compose -f docker-compose.local.yml -f deployments/docker-compose.ha.yml --env-file .env.$(VARIANT) rm -sf postgres-replica redis-replica

ha-status: ## Replication status: PG slot/streaming/lag + Redis link
	@scripts/replication/replication-status.sh

replica-drill: ## HA drill: PG streaming + read/write split + Redis promote & fail-back
	@scripts/replication/drill-ha.sh $(DRILL_ARGS)

b4-verify: ## Run the B4 acceptance chain (QUICK=1 for a fast smoke)
	@test -n "$(QUICK)" && scripts/b4-verify.sh --quick || scripts/b4-verify.sh

backup-db: ## Daily PostgreSQL backup (dump per schema + SHA256)
	@scripts/backup-db.sh

restore-db: ## Restore drill: make restore-db STAMP=backups/b4-*/<stamp>
	@test -n "$(STAMP)" || { echo "STAMP is required, e.g. make restore-db STAMP=backups/<ts>"; exit 1; }
	@scripts/restore-db.sh "$(STAMP)"

backup-redis: ## Redis BGSAVE snapshot backup (best-effort)
	@scripts/backup-redis.sh

retention-purge: ## Retention sweep of telemetry partitions (APPLY=1 to drop)
	@test -n "$(APPLY)" && scripts/retention-purge.sh --apply || scripts/retention-purge.sh

querybench: ## Query SLA bench (30-day history < 1.5 s, geofence < 500 ms)
	@(cd tools/querybench && go run . --company=$(or $(CODE),DEV001))