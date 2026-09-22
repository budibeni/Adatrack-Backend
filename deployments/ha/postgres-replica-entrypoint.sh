#!/bin/sh
# ============================================================================
# postgres-replica-entrypoint.sh — bootstrap + start the PG standby (PRD §13)
# ============================================================================
# Dipakai container `postgres-replica` (deployments/docker-compose.ha.yml).
#
# PGDATA kosong  : ambil base backup dari primary dengan `pg_basebackup -R`,
#                  yang menulis standby.signal + primary_conninfo (termasuk
#                  primary_slot_name), jadi start berikutnya cukup melanjutkan
#                  streaming dari WAL slot yang sama.
# PGDATA terisi  : langsung start sebagai standby (resume, tanpa base backup).
#
# Env: PRIMARY_HOST (default `postgres`), POSTGRES_USER, PGPASSWORD (untuk
# autentikasi pg_basebackup), REPLICATION_SLOT (default `pg_replica_slot`).
# Slot + baris pg_hba `host replication ...` DISIAPKAN oleh
# scripts/replication/drill-ha.sh di sisi primary (pg_basebackup gagal bila
# slot belum ada).
# ============================================================================
set -eu

PGDATA="${PGDATA:-/var/lib/postgresql/data}"
PRIMARY_HOST="${PRIMARY_HOST:-postgres}"
REPLICATION_SLOT="${REPLICATION_SLOT:-pg_replica_slot}"

# Volume mount pertama kali dimiliki root; base backup harus jalan sebagai
# postgres supaya berkasnya bisa dibaca server. Pola ini sama dengan
# docker-entrypoint.sh resmi image postgres.
if [ "$(id -u)" = '0' ]; then
  mkdir -p "$PGDATA"
  chown -R postgres:postgres "$PGDATA"
  exec gosu postgres "$0" "$@"
fi

if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "ha: PGDATA kosong — menunggu primary $PRIMARY_HOST"
  for _ in $(seq 1 60); do
    pg_isready -h "$PRIMARY_HOST" -p 5432 -U "$POSTGRES_USER" >/dev/null 2>&1 && break
    sleep 2
  done

  echo "ha: pg_basebackup dari $PRIMARY_HOST (slot $REPLICATION_SLOT)"
  find "$PGDATA" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  pg_basebackup \
    --host="$PRIMARY_HOST" --port=5432 --username="$POSTGRES_USER" \
    --pgdata="$PGDATA" --format=plain --wal-method=stream \
    --slot="$REPLICATION_SLOT" --write-recovery-conf --progress
  chmod 700 "$PGDATA"
fi

echo "ha: start standby"
exec docker-entrypoint.sh postgres
