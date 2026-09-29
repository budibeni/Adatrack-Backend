#!/usr/bin/env bash
# ============================================================================
# migrate.sh — automatic, idempotent database migrations (PRD §14.5)
# ============================================================================
# Coolify pre-deploy hook (pre_deployment_command). Runs, in order, fail-fast —
# a failure ABORTS the deploy (no service ever runs against a stale schema):
#   1. wait for PostgreSQL (retry + backoff)
#   2. bootstrap schemas            (database/init-pg/01_schemas.sql)
#   3. master migrations            (ledger-audited, checksum-guarded)
#   4. master setup + dev tenant    (database/init-pg/02_master_setup.sql)
#   5. reference seed               (wilayah Indonesia, idempotent)
#   6. company template             → platform `default` tenant schema
#   7. dev tenant seed (DEV001) + role-menu access
#   8. ALL existing tenant schemas  (company migrations — no tenant left behind)
#   9. ledger verification          (applied == files, failures == 0)
#
# Usage: scripts/migrate.sh [local|coolify]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

load_variant_env "${1:-${COMPOSE_VARIANT:-local}}"

"$ROOT/scripts/pg-wait.sh" 60 >&2

# tenant_schemas: every existing COMPANY schema (i.e. all `adatrack_gps_*`
# except the master). Migrations must cover EVERY tenant, not just the two
# bootstrap ones — a tenant created earlier silently drifts when a new company
# migration lands. That is not theoretical: on 2026-09-29 `adatrack_gps_loadt2`
# sat at max 017 while the deployment was at 027 (10 migrations behind) because
# this script used to hard-code default + dev001 only. Enumerating pg_namespace
# (instead of the registry) guarantees we only ever migrate schemas that exist,
# so a soft-deleted/dropped tenant can never be resurrected.
tenant_schemas() {
  psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q -c \
    "SELECT nspname FROM pg_namespace \
     WHERE nspname LIKE '${COMPANY_DB_PREFIX:-adatrack_gps_}%' \
       AND nspname <> '${MASTER_SCHEMA}' ORDER BY nspname;"
}

echo "migrate: [1/8] bootstrap schemas"
psql_q -f "$ROOT/database/init-pg/01_schemas.sql"

echo "migrate: [2/8] master migrations"
apply_dir master "$MASTER_SCHEMA" "$MIGRATIONS_MASTER"

echo "migrate: [3/8] master setup (platform + dev tenant)"
psql_q -v migrations_dir="$ROOT/database/migrations" -v skip_migrations=1 \
  -f "$ROOT/database/init-pg/02_master_setup.sql"

echo "migrate: [4/8] reference seed (wilayah Indonesia)"
seed_reference

echo "migrate: [5/8] company template (platform default tenant)"
apply_dir "company:adatrack_gps_default" adatrack_gps_default "$MIGRATIONS_COMPANY"

echo "migrate: [6/8] dev tenant (DEV001)"
apply_dir "company:adatrack_gps_dev001" adatrack_gps_dev001 "$MIGRATIONS_COMPANY"
psql_q -v migrations_dir="$ROOT/database/migrations" -v skip_migrations=1 \
  -f "$ROOT/database/init-pg/03_company_setup.sql"

echo "migrate: [7/8] all existing tenant schemas (company migrations)"
while IFS= read -r tenant_schema; do
  [[ -z "$tenant_schema" ]] && continue
  echo "migrate:   tenant ${tenant_schema}"
  apply_dir "company:${tenant_schema}" "$tenant_schema" "$MIGRATIONS_COMPANY"
done < <(tenant_schemas)

echo "migrate: [8/8] ledger verification"
ledger_report "$MASTER_SCHEMA"
while IFS= read -r tenant_schema; do
  [[ -z "$tenant_schema" ]] && continue
  ledger_report "$tenant_schema"
done < <(tenant_schemas)

echo "migrate: done"
