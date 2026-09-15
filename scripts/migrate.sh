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
#   7. ledger verification          (applied == files, failures == 0)
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

echo "migrate: [1/7] bootstrap schemas"
psql_q -f "$ROOT/database/init-pg/01_schemas.sql"

echo "migrate: [2/7] master migrations"
apply_dir master "$MASTER_SCHEMA" "$MIGRATIONS_MASTER"

echo "migrate: [3/7] master setup (platform + dev tenant)"
psql_q -v migrations_dir="$ROOT/database/migrations" -v skip_migrations=1 \
  -f "$ROOT/database/init-pg/02_master_setup.sql"

echo "migrate: [4/7] reference seed (wilayah Indonesia)"
seed_reference

echo "migrate: [5/7] company template (platform default tenant)"
apply_dir "company:adatrack_gps_default" adatrack_gps_default "$MIGRATIONS_COMPANY"

echo "migrate: [6/7] dev tenant (DEV001)"
apply_dir "company:adatrack_gps_dev001" adatrack_gps_dev001 "$MIGRATIONS_COMPANY"
psql_q -v migrations_dir="$ROOT/database/migrations" -v skip_migrations=1 \
  -f "$ROOT/database/init-pg/03_company_setup.sql"

echo "migrate: [7/7] ledger verification"
ledger_report "$MASTER_SCHEMA"
ledger_report adatrack_gps_dev001

echo "migrate: done"
