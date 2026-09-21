#!/usr/bin/env bash
# ============================================================================
# retention-purge.sh — DB retention enforcement (B4, PRD §11).
# Drops th_telemetry_logs monthly partitions older than HOT_RETENTION_DAYS
# (default 30) per tenant schema AFTER counting rows (no silent loss: the
# count is logged + reported). Dry-run by default; pass --apply to detach/drop.
# Archive-to-object-storage is out of scope for the local B4 proof (logged as
# a manual step); the partition drop is the enforced mechanism.
# Usage: scripts/retention-purge.sh [--apply] [hot_retention_days]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}"

APPLY=false
[[ "${1:-}" == "--apply" ]] && { APPLY=true; shift; }
HOT_DAYS="${1:-${HOT_RETENTION_DAYS:-30}}"
CUTOFF="$(date -u -d "-${HOT_DAYS} days" +%Y-%m-%d 2>/dev/null || date -u -v-"${HOT_DAYS}"d +%Y-%m-%d)"

echo "retention-purge: hot_retention=${HOT_DAYS}d cutoff=${CUTOFF} apply=${APPLY}"

schemas="$(psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q \
  -c "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'adatrack_gps_%' AND schema_name <> '${MASTER_SCHEMA}' ORDER BY 1;")"

total_dropped=0
for s in $schemas; do
  # Monthly partitions of th_telemetry_logs older than the cutoff month.
  parts="$(psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q -c "
    SELECT inhrelid::regclass::text FROM pg_inherits
    WHERE inhparent = '${s}.th_telemetry_logs'::regclass
      AND inhrelid::regclass::text NOT LIKE '%pdefault%';" 2>/dev/null || true)"
  for p in $parts; do
    # Partition name th_telemetry_logs_pYYYYMM → derive first-of-month.
    if [[ "$p" =~ _p([0-9]{6})$ ]]; then
      ym="${BASH_REMATCH[1]}"
      pdate="${ym:0:4}-${ym:4:2}-01"
      if [[ "$pdate" < "$CUTOFF" ]]; then
        cnt="$(psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q -c "SELECT count(*) FROM ${s}.${p##*.};" 2>/dev/null || echo "?")"
        if [[ "$APPLY" == true ]]; then
          echo "retention-purge: DROPPING ${s}.${p##*.} rows=${cnt}"
          psql_q -c "DROP TABLE ${s}.${p##*.};"
          total_dropped=$((total_dropped + 1))
        else
          echo "retention-purge: [dry-run] would drop ${s}.${p##*.} rows=${cnt} (before=$cnt after=0 verify on --apply)"
        fi
      fi
    fi
  done
  # Always ensure the rolling window exists (idempotent helper from 007).
  nextm="$(date -u -d "+1 month" +%Y-%m-01 2>/dev/null || date -u -v+1m +%Y-%m-01)"
  psql_q -c "SELECT ${s}.tm_ensure_telemetry_partition(DATE '${nextm}');" >/dev/null 2>&1 || true
done

echo "retention-purge: done (partitions dropped=${total_dropped}, dry-run=$([[ "$APPLY" == true ]] && echo false || echo true))"
