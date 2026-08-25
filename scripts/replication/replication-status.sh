#!/usr/bin/env bash
# ============================================================================
# replication-status.sh — Cek kesehatan replikasi MySQL, PostgreSQL & Redis
# (runbook §5 #9).
#
# Catatan: backend/.env tidak di-source utuh (aman thd. karakter khusus pada
# secret); hanya kunci yang dibutuhkan dibaca. OS env selalu menang.
# ============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/.env}"

# env_of <KEY> [default] — OS env > backend/.env > default.
env_of() {
  local k="$1" d="${2:-}" v=""
  if [[ -n "${!k:-}" ]]; then printf '%s' "${!k}"; return; fi
  if [[ -f "$ENV_FILE" ]]; then
    v="$(grep -E "^[[:space:]]*${k}[[:space:]]*=" "$ENV_FILE" | tail -n1 | sed -E 's/^[^=]*=[[:space:]]*//' | tr -d '"' | tr -d "'" | tr -d '[:space:]')"
  fi
  printf '%s' "${v:-$d}"
}

PG_USER="$(env_of POSTGRES_USER adatrack_gps_user)"
PG_DB="$(env_of POSTGRES_DB adatrack_gps_db)"

echo "== MySQL replica status =="
MYSQL_PWD="$(env_of MYSQL_ROOT_PASSWORD '')"
if [[ -n "$MYSQL_PWD" ]] && docker ps --format '{{.Names}}' | grep -q '^mysql_replica$'; then
  docker exec -e MYSQL_PWD="$MYSQL_PWD" mysql_replica sh -c 'mysql -uroot -e "SHOW REPLICA STATUS\G"' \
    | grep -E 'Replica_IO_Running|Replica_SQL_Running|Seconds_Behind|Last_Error' \
    || echo "(replica belum ter-setup — jalankan setup-mysql-replication.sh)"
else
  echo "(mysql_replica tidak berjalan — skip; jalankan setup-mysql-replication.sh bila perlu)"
fi

echo; echo "== PostgreSQL replication status =="
PQ=(docker exec postgres psql -U "$PG_USER" -d "$PG_DB")
RQ=(docker exec postgres_replica psql -U "$PG_USER" -d "$PG_DB")
if docker ps --format '{{.Names}}' | grep -q '^postgres$'; then
  OUT="$("${PQ[@]}" -tAc "SELECT application_name||' | state='||state||' | sync='||sync_state||' | lag='||COALESCE(replay_lag::text,'-') FROM pg_stat_replication;" || true)"
  if [[ -n "$OUT" ]]; then printf '%s\n' "$OUT" | sed 's/^/primary → /';
  else echo "(belum ada standby terhubung — jalankan setup-postgres-replication.sh / cek postgres_replica)"; fi
else
  echo "(primary postgres tidak berjalan)"
fi
if docker ps --format '{{.Names}}' | grep -q '^postgres_replica$'; then
  "${RQ[@]}" -tAc "SELECT 'in_recovery='||pg_is_in_recovery()||' | last_replay='||COALESCE(pg_last_xact_replay_timestamp()::text,'-');" | sed 's/^/replica → /'
else
  echo "(postgres_replica tidak berjalan)"
fi

echo; echo "== Redis replica status =="
if docker ps --format '{{.Names}}' | grep -q '^redis_replica$'; then
  docker exec redis_replica redis-cli INFO replication | grep -E '^role|^master_host|^master_link_status|^master_sync_in_progress' || true
else
  echo "(redis_replica tidak berjalan)"
fi

echo; echo "== Containers replikasi =="
docker ps --format '{{.Names}}\t{{.Status}}' | grep -E '^(mysql|redis|postgres|mysql_replica|redis_replica|postgres_replica)\b' || true