#!/usr/bin/env bash
# ============================================================================
# pg-wait.sh — block until PostgreSQL accepts connections (PRD §14.5 retry+backoff)
# ============================================================================
# Usage: pg-wait.sh [attempts] [sleep_seconds]
# Honors PGHOST/PGPORT/PGUSER/PGDATABASE or POSTGRES_* env (compose/service).
# ============================================================================
set -euo pipefail

attempts="${1:-60}"
sleep_secs="${2:-2}"

export PGHOST="${PGHOST:-${POSTGRES_HOST:-127.0.0.1}}"
export PGPORT="${PGPORT:-${POSTGRES_PORT:-5432}}"
export PGUSER="${PGUSER:-${POSTGRES_USER:-adatrack}}"
export PGDATABASE="${PGDATABASE:-${POSTGRES_DB:-adatrack_gps_db}}"
export PGPASSWORD="${PGPASSWORD:-${POSTGRES_PASSWORD:-}}"

for ((i = 1; i <= attempts; i++)); do
  if pg_isready -q -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE"; then
    echo "pg-wait: ready after ${i} attempt(s) (${PGHOST}:${PGPORT}/${PGDATABASE})"
    exit 0
  fi
  # exponential-ish backoff capped at 10s (1,2,3,5,8,10,10,...)
  wait="$sleep_secs"
  case "$i" in
    1) wait=1 ;;
    2) wait=2 ;;
    3) wait=3 ;;
    4) wait=5 ;;
    5) wait=8 ;;
    *) wait=10 ;;
  esac
  echo "pg-wait: attempt ${i}/${attempts} not ready, retrying in ${wait}s" >&2
  sleep "$wait"
done

echo "pg-wait: PostgreSQL not ready after ${attempts} attempts" >&2
exit 1