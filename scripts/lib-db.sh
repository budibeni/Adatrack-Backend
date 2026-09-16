#!/usr/bin/env bash
# ============================================================================
# lib-db.sh — shared PostgreSQL helpers for backend scripts (PRD §14.5)
# ============================================================================
# Sourced by scripts/migrate.sh and scripts/provision-tenant.sh.
# Not executable on its own.
# ============================================================================
# shellcheck shell=bash

# load_variant_env <local|coolify>: loads .env.<variant> and derives PG* env.
load_variant_env() {
  local variant="${1:-${COMPOSE_VARIANT:-local}}"
  local env_file="$ROOT/.env.${variant}"
  if [[ ! -f "$env_file" ]]; then
    echo "db: missing env file $env_file" >&2
    return 1
  fi
  set -a
  # shellcheck disable=SC1090
  . "$env_file"
  set +a

  # Running on the host (not inside compose)? the compose service name and the
  # in-container port from the env file are meaningless out here — OVERRIDE with
  # the published bind host/port (HOST_* in .env.<variant>). A plain ":=" default
  # is not enough: .env.local sets POSTGRES_PORT=5432 (container-internal), which
  # would silently point host-side psql at whatever else listens on host 5432.
  if [[ "${POSTGRES_HOST:-}" == "postgres" && ! -f /.dockerenv ]]; then
    POSTGRES_HOST="127.0.0.1"
    POSTGRES_PORT="${HOST_PG_PORT:-5533}"
  fi

  export PGHOST="$POSTGRES_HOST"
  export PGPORT="$POSTGRES_PORT"
  export PGUSER="$POSTGRES_USER"
  export PGDATABASE="$POSTGRES_DB"
  export PGPASSWORD="$POSTGRES_PASSWORD"

  MASTER_SCHEMA="${MASTER_DB_NAME:-adatrack_gps_master}"
  LEDGER="${MIGRATION_LEDGER_TABLE:-tm_schema_migrations}"
  MIGRATIONS_MASTER="$ROOT/database/migrations/master_pg"
  MIGRATIONS_COMPANY="$ROOT/database/migrations/company_pg"
  SEED_DIR="$ROOT/database/seed/reference"
}

psql_q() { psql -v ON_ERROR_STOP=1 --no-psqlrc -X -q "$@"; }

# now_ms prints the current time in milliseconds. `date +%s%3N` is not portable
# (some date implementations ignore the %3 modifier and emit full nanoseconds,
# which overflows a 32-bit duration column), so %N is normalized explicitly and
# a second-resolution fallback is used when %N is unsupported.
now_ms() {
  local secs ns
  secs="$(date +%s)"
  ns="$(date +%N 2>/dev/null || echo "")"
  if [[ "$ns" =~ ^[0-9]{1,9}$ ]]; then
    echo $(( secs * 1000 + 10#$ns / 1000000 ))
  else
    echo $(( secs * 1000 ))
  fi
}

# ensure_ledger <schema>: idempotently create the migration ledger (§14.5).
ensure_ledger() {
  local schema="$1"
  psql_q -c "CREATE SCHEMA IF NOT EXISTS ${schema};"
  psql_q -c "CREATE TABLE IF NOT EXISTS ${schema}.${LEDGER} (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope VARCHAR(64) NOT NULL,
    version VARCHAR(255) NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    success BOOLEAN NOT NULL DEFAULT TRUE,
    applied_by VARCHAR(64) NOT NULL DEFAULT CURRENT_USER,
    CONSTRAINT uq_schema_migrations UNIQUE (scope, version)
);"
}

# apply_dir <scope> <schema> <dir>: versioned, checksum-guarded, idempotent apply.
# Re-applying a file with a DIFFERENT checksum aborts (immutability, §14.5).
apply_dir() {
  local scope="$1" schema="$2" dir="$3"
  local count=0
  ensure_ledger "$schema"
  shopt -s nullglob
  local f version checksum ledger_checksum start ended
  for f in $(ls -1 "$dir"/*.sql | sort); do
    version="$(basename "$f")"
    checksum="$(sha256sum "$f" | awk '{print $1}')"
    ledger_checksum="$(psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q \
      -c "SELECT checksum FROM ${schema}.${LEDGER} WHERE scope='${scope}' AND version='${version}' AND success;" \
      | tr -d '[:space:]')"
    if [[ -n "$ledger_checksum" ]]; then
      if [[ "$ledger_checksum" != "$checksum" ]]; then
        echo "db: CHECKSUM DRIFT for ${scope}/${version} (ledger=$ledger_checksum file=$checksum)" >&2
        echo "db: migrations are immutable — add a NEW versioned file instead" >&2
        return 1
      fi
      echo "db:   skip (already applied) ${scope}/${version}" >&2
      continue
    fi
    echo "db:   apply ${scope}/${version}" >&2
    start=$(now_ms)
    # search_path forced per session so migration files stay schema-agnostic.
    PGOPTIONS="-c search_path=${schema}" psql -v ON_ERROR_STOP=1 --no-psqlrc -X -q -f "$f"
    ended=$(now_ms)
    psql_q -c "INSERT INTO ${schema}.${LEDGER} (scope, version, checksum, duration_ms, success) \
      VALUES ('${scope}', '${version}', '${checksum}', $((ended - start)), TRUE) \
      ON CONFLICT (scope, version) DO UPDATE SET checksum = EXCLUDED.checksum, \
        applied_at = CURRENT_TIMESTAMP, duration_ms = EXCLUDED.duration_ms, success = TRUE;"
    count=$((count + 1))
  done
  echo "db: ${scope}: ${count} new migration(s) applied" >&2
}

# ledger_report <schema>: prints applied/failure counts (deploy verification).
ledger_report() {
  local schema="$1"
  psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q -c "SELECT scope, \
    count(*) AS applied, count(*) FILTER (WHERE NOT success) AS failures \
    FROM ${schema}.${LEDGER} GROUP BY scope ORDER BY scope;"
}

# seed_reference: applies the wilayah Indonesia seed into the master schema.
seed_reference() {
  shopt -s nullglob
  local f
  for f in $(ls -1 "$SEED_DIR"/*.sql | sort); do
    echo "db:   seed $(basename "$f")" >&2
    PGOPTIONS="-c search_path=${MASTER_SCHEMA}" psql -v ON_ERROR_STOP=1 --no-psqlrc -X -q -f "$f"
  done
}