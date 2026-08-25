#!/usr/bin/env bash
# ============================================================================
# setup-postgres-replication.sh — Pasang replikasi streaming PRIMARY → REPLICA
# PostgreSQL (padanan setup-mysql-replication.sh, docs/HIGH_AVAILABILITY.md).
#
# IDEMPOTEN — aman diulang. Menangani dua kondisi:
#   • Volume primary FRESH : role+pg_hba sudah dibuat otomatis oleh init-script
#     backend/database/init-pg/04-create-replicator-role.sh → no-op.
#   • Volume primary LAMA  : role/pg_hba ditambahkan di sini tanpa duplikat,
#     lalu config di-reload (pg_reload_conf).
#
# Asumsi (rehearsal 1-host): container "postgres" (primary) sudah UP;
# "postgres_replica" dari deployments/docker-compose.ha.yml bootstrap sendiri
# via replica-entrypoint.sh (pg_basebackup -R + slot).
#
# Catatan: .env TIDAK di-source utuh (aman thd. karakter khusus pada secret —
# mis. '|' pada JWT_SECRET); hanya kunci yang dibutuhkan yang dibaca, dan
# OS env selalu menang atas file (konsisten dgn internal.LoadProjectEnv()).
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
REPL_USER="$(env_of POSTGRES_REPLICA_USER replicator)"
REPL_PASS="$(env_of POSTGRES_REPLICA_PASSWORD '')"
SLOT="$(env_of POSTGRES_REPLICATION_SLOT pg_replica_slot)"
[ -n "$REPL_PASS" ] || { echo '[setup] ERROR: POSTGRES_REPLICA_PASSWORD wajib di-set (.env / OS env)' >&2; exit 1; }

echo "[1/3] Primary: pastikan role '${REPL_USER}' + pg_hba replication ..."
docker exec postgres psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
    -v ru="$REPL_USER" -v rp="$REPL_PASS" <<'EOSQL'
SELECT format('CREATE ROLE %I WITH LOGIN REPLICATION PASSWORD %L', :'ru', :'rp')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'ru') \gexec
EOSQL
docker exec postgres bash -s <<'EHBA'
LINE="host replication ${POSTGRES_REPLICA_USER} all scram-sha-256"
grep -qsF "$LINE" "$PGDATA/pg_hba.conf" || printf '\n%s\n' "$LINE" >> "$PGDATA/pg_hba.conf"
psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc 'SELECT pg_reload_conf();' >/dev/null
EHBA

echo "[2/3] Primary: pastikan physical replication slot '${SLOT}' ..."
docker exec postgres psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
    -v sl="$SLOT" <<'EOSQL'
SELECT pg_create_physical_replication_slot(:'sl')
WHERE NOT EXISTS (SELECT FROM pg_replication_slots WHERE slot_name = :'sl');
EOSQL

echo "[3/3] Snapshot replikasi ..."
if docker ps --format '{{.Names}}' | grep -q '^postgres_replica$'; then
  printf 'replica in_recovery = '
  docker exec postgres_replica psql -U "$PG_USER" -d "$PG_DB" -tAc 'SELECT pg_is_in_recovery();'
fi
docker exec postgres psql -U "$PG_USER" -d "$PG_DB" -P pager=off \
  -c "SELECT application_name, state, sync_state, COALESCE(replay_lag::text,'-') AS lag FROM pg_stat_replication;"

echo "[setup] SELESAI — verifikasi dengan ./replication-status.sh"
