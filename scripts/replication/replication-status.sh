#!/usr/bin/env bash
# ============================================================================
# replication-status.sh — status replikasi PostgreSQL + Redis (PRD §13)
# ============================================================================
# Baca-saja: menampilkan slot WAL, state streaming, lag, dan link replika Redis.
# Dipakai saat diagnosis (docs/HIGH_AVAILABILITY.md §5, alert PgSlot*).
#
# Usage: scripts/replication/replication-status.sh
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}" >/dev/null

PG_PRIMARY="${PG_PRIMARY_CONTAINER:-adatrack_postgres}"
PG_REPLICA="${PG_REPLICA_CONTAINER:-adatrack_postgres_replica}"
REDIS_REPLICA_PORT="${HOST_REDIS_REPLICA_PORT:-6381}"

pgq() { docker exec -i "$1" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-psqlrc -X -tA -c "$2" 2>/dev/null; }

echo '== PostgreSQL — PRIMARY =='
if docker ps --format '{{.Names}}' | grep -qx "$PG_PRIMARY"; then
  echo '-- slot WAL --'
  pgq "$PG_PRIMARY" "SELECT slot_name || ' | active=' || active || ' | restart_lsn=' || COALESCE(restart_lsn::text,'-') FROM pg_replication_slots;" || echo '  (tidak ada slot)'
  echo '-- standby terhubung --'
  pgq "$PG_PRIMARY" "SELECT application_name || ' | ' || state || ' | ' || sync_state || ' | wal_diff_bytes=' || COALESCE(pg_wal_lsn_diff(sent_lsn, replay_lsn)::bigint::text,'-') FROM pg_stat_replication;" \
    | sed '/^$/d' || true
else
  echo "  container $PG_PRIMARY tidak jalan"
fi

echo '== PostgreSQL — REPLICA =='
if docker ps --format '{{.Names}}' | grep -qx "$PG_REPLICA"; then
  echo "-- in_recovery=$(pgq "$PG_REPLICA" 'SELECT pg_is_in_recovery();') --"
  echo '-- wal receiver --'
  pgq "$PG_REPLICA" "SELECT COALESCE(status,'-') || ' | sender=' || COALESCE(sender_host,'-') || ' | latest_end_lsn=' || COALESCE(latest_end_lsn::text,'-') FROM pg_stat_wal_receiver;" || echo '  (belum ada wal receiver)'
else
  echo "  container $PG_REPLICA tidak jalan (jalankan: make ha-up)"
fi

echo '== Redis =='
printf 'primary : %s\n' "$(redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" INFO replication 2>/dev/null | grep -E '^role:' | tr -d '\r' || echo 'tidak jalan')"
if redis-cli -h 127.0.0.1 -p "$REDIS_REPLICA_PORT" ping >/dev/null 2>&1; then
  redis-cli -h 127.0.0.1 -p "$REDIS_REPLICA_PORT" INFO replication 2>/dev/null | grep -E '^role:|^master_link_status:|^master_last_io_seconds_ago:|^slave_repl_offset:' | tr -d '\r' | sed 's/^/replica : /' || true
else
  echo "replica : tidak jalan (port $REDIS_REPLICA_PORT)"
fi
