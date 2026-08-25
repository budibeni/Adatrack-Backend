#!/bin/bash
# ============================================================================
# 04-create-replicator-role.sh — init PRIMARY PostgreSQL (PRD §7.1.1 / HA).
#
# Bagian dari docker-entrypoint-initdb.d service `postgres`
# (backend/docker-compose.yml) — dieksekusi OTOMATIS oleh entrypoint resmi
# postgres SAAT PERTAMA volume primary di-initialisasi:
#   1. membuat role khusus streaming replication;
#   2. membuka pg_hba.conf untuk koneksi replication dari host replika.
#
# Untuk volume primary LAMA (init sudah lewat), jalankan ulang secara
# idempoten lewat backend/scripts/replication/setup-postgres-replication.sh.
# ============================================================================
set -e

REPL_USER="${POSTGRES_REPLICA_USER:-replicator}"
REPL_PASS="${POSTGRES_REPLICA_PASSWORD:?[pg-init-primary] POSTGRES_REPLICA_PASSWORD wajib di-set (.env)}"

echo "[pg-init-primary] memastikan role replikasi '${REPL_USER}' ..."
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -v repl_user="$REPL_USER" -v repl_pass="$REPL_PASS" <<'EOSQL'
SELECT format('CREATE ROLE %I WITH LOGIN REPLICATION PASSWORD %L', :'repl_user', :'repl_pass')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'repl_user') \gexec
EOSQL

echo "[pg-init-primary] membuka pg_hba replication untuk '${REPL_USER}' ..."
PGHBA="${PGDATA}/pg_hba.conf"
LINE="host replication ${REPL_USER} all scram-sha-256"
if ! grep -qsF "$LINE" "$PGHBA"; then
  printf '\n# Streaming replication (deployments/docker-compose.ha.yml)\n%s\n' "$LINE" >> "$PGHBA"
fi

echo "[pg-init-primary] SELESAI (role + pg_hba siap)."