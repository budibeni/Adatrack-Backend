#!/bin/bash
# ============================================================================
# replica-entrypoint.sh — bootstrap READ REPLICA PostgreSQL streaming.
#
# Entry point pengganti pada service `postgres-replica`
# (backend/deployments/docker-compose.ha.yml). Saat PGDATA kosong:
#   1. menunggu primary reachable,
#   2. membersihkan replication slot lama bernama sama (bootstrap ulang aman),
#   3. `pg_basebackup -R` → clone fisik + standby.signal + primary_conninfo
#      (+ replication slot agar WAL tidak hilang saat replica mati lama),
#   4. exec entrypoint resmi → server start READ-ONLY (melayani SELECT;
#      seluruh tulisan masuk lewat PRIMARY — pola read/write split §7.1.1).
#
# Koneksi primary memakai host.docker.internal:<POSTGRES_PORT> — pola yang
# sama dengan redis-replica (tanpa depends_on lintas file compose).
# ============================================================================
set -euo pipefail

PGDATA_DIR="${PGDATA:-/var/lib/postgresql/data}"
SRC_HOST="${REPLICA_SOURCE_HOST:-host.docker.internal}"
SRC_PORT="${REPLICA_SOURCE_PORT:-5432}"
REPL_USER="${POSTGRES_REPLICA_USER:-replicator}"
REPL_PASS="${POSTGRES_REPLICA_PASSWORD:?[pg-replica] POSTGRES_REPLICA_PASSWORD wajib di-set (.env)}"
SLOT="${REPLICATION_SLOT:-pg_replica_slot}"

if [ -s "$PGDATA_DIR/PG_VERSION" ]; then
  echo "[pg-replica] PGDATA sudah terisi — pastikan .pgpass tetap ada lalu start standby."
else
  echo "[pg-replica] PGDATA kosong — bootstrap dari ${SRC_HOST}:${SRC_PORT} ..."
fi

# .pgpass dipasang SELALU (bukan hanya saat bootstrap): file ini berada DI LUAR
# volume PGDATA sehingga hilang tiap container direcreate, padahal
# primary_conninfo (hasil pg_basebackup -R) mereferensikannya. Tanpa ini,
# walreceiver gagal 'fe_sendauth: no password supplied' setelah recreate.
export PGPASSFILE="/var/lib/postgresql/.pgpass"
printf '*:*:*:%s:%s\n' "$REPL_USER" "$REPL_PASS" > "$PGPASSFILE"
chmod 600 "$PGPASSFILE"
chown postgres:postgres "$PGPASSFILE"

if [ ! -s "$PGDATA_DIR/PG_VERSION" ]; then
  until psql -h "$SRC_HOST" -p "$SRC_PORT" -U "$REPL_USER" -d postgres \
        -tAc 'SELECT 1' >/dev/null 2>&1; do
    echo "[pg-replica] primary belum siap — retry 3 dtk ..."
    sleep 3
  done

  # Bootstrap ulang harus deterministik: buang slot lama bernama sama di
  # primary bila ada (slot ini dedikasi hanya untuk replica ini).
  psql -h "$SRC_HOST" -p "$SRC_PORT" -U "$REPL_USER" -d postgres -tAc \
    "SELECT pg_drop_replication_slot('${SLOT}') FROM pg_replication_slots WHERE slot_name='${SLOT}'" \
    >/dev/null 2>&1 || true

  pg_basebackup -h "$SRC_HOST" -p "$SRC_PORT" -U "$REPL_USER" \
    -D "$PGDATA_DIR" -X stream -P -R -C -S "$SLOT"

  chown -R postgres:postgres "$PGDATA_DIR"
  chmod 700 "$PGDATA_DIR"
  echo "[pg-replica] basebackup selesai (standby.signal + slot '${SLOT}')."
fi

exec /usr/local/bin/docker-entrypoint.sh "$@"
