#!/usr/bin/env bash
# ============================================================================
# reset-db.sh — drop every ADATRACK schema and re-provision from scratch
# ============================================================================
# DESTRUCTIVE (data loss) — dev/test only.
#   scripts/reset-db.sh            # drop schemas + re-run migrations
#   scripts/reset-db.sh --volumes  # also `compose down -v` (wipes PG/Redis/NATS)
#
# Only schemas matching the COMPANY_DB_PREFIX / master schema are dropped, so a
# shared PostgreSQL instance is never wiped wholesale.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

load_variant_env "${COMPOSE_VARIANT:-local}"

if [[ "${1:-}" == "--volumes" ]]; then
  if [[ -x "$ROOT/scripts/compose-up.sh" ]]; then
    echo "reset-db: compose down -v (infra volumes)"
    "$ROOT/scripts/compose-up.sh" down -v || true
  fi
fi

"$ROOT/scripts/pg-wait.sh" 60 >&2

PREFIX="${COMPANY_DB_PREFIX:-adatrack_gps_}"
echo "reset-db: dropping schemas matching '${PREFIX}%'"
psql -v ON_ERROR_STOP=1 --no-psqlrc -X -q -c "
DO \$\$
DECLARE s RECORD;
BEGIN
  FOR s IN
    SELECT schema_name FROM information_schema.schemata
    WHERE schema_name LIKE '${PREFIX}%' OR schema_name = '${MASTER_SCHEMA}'
  LOOP
    EXECUTE format('DROP SCHEMA IF EXISTS %I CASCADE', s.schema_name);
    RAISE NOTICE 'reset-db: dropped %', s.schema_name;
  END LOOP;
END \$\$;"

echo "reset-db: re-running migrations"
"$ROOT/scripts/migrate.sh" "${COMPOSE_VARIANT:-local}"
echo "reset-db: done"