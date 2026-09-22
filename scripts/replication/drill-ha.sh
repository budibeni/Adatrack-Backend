#!/usr/bin/env bash
# ============================================================================
# drill-ha.sh — drill replikasi PostgreSQL + Redis (PRD §13, §14.6)
# ============================================================================
# Membuktikan pada stack lokal NYATA (bukan mock) bahwa:
#   1. standby PostgreSQL streaming dari slot `pg_replica_slot`
#   2. penulisan primary TERPROPAGASI ke standby (INSERT → replika)
#   3. tulis langsung ke standby DITOLAK (read-only)
#   4. lag replikasi ~0 dan slot aktif
#   5. replika Redis link-up dan menerima propagasi
#   6. drill failover Redis: promote → menerima tulis → fail-back resync
#
# Usage: scripts/replication/drill-ha.sh [--keep] [--quick]
#   --keep  : biarkan container replika hidup setelah drill
#   --quick : lewati langkah promote/fail-back Redis
#
# Container replika dinaikkan dari deployments/docker-compose.ha.yml.
# ============================================================================
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}" >/dev/null

KEEP=0; QUICK=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    --quick) QUICK=1 ;;
    *) echo "drill-ha: argumen tidak dikenal '$arg'" >&2; exit 2 ;;
  esac
done

PG_PRIMARY="${PG_PRIMARY_CONTAINER:-adatrack_postgres}"
PG_REPLICA="${PG_REPLICA_CONTAINER:-adatrack_postgres_replica}"
REDIS_REPLICA_PORT="${HOST_REDIS_REPLICA_PORT:-6381}"
REDIS_REPLICA_HOST="127.0.0.1"
ENV_FILE="$ROOT/.env.${COMPOSE_VARIANT:-local}"
SLOT="pg_replica_slot"
MARKER_TABLE="${MASTER_DB_NAME:-adatrack_gps_master}.ha_drill_marker"
COMPOSE=(docker compose -f "$ROOT/docker-compose.local.yml" -f "$ROOT/deployments/docker-compose.ha.yml" --env-file "$ENV_FILE")

pass=0; fail=0
ok()   { echo "HA-PASS: $1"; pass=$((pass + 1)); }
bad()  { echo "HA-FAIL: $1"; fail=$((fail + 1)); }
step() { echo; echo "===== HA [$1] $2 ====="; }

pgq() { docker exec -i "$1" psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-psqlrc -X -tA -c "$2" 2>&1; }
rcli() { redis-cli -h "$REDIS_REPLICA_HOST" -p "$REDIS_REPLICA_PORT" "$@" 2>&1; }
rprim() { redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" "$@" 2>&1; }

cleanup() {
  pgq "$PG_PRIMARY" "DROP TABLE IF EXISTS ${MARKER_TABLE};" >/dev/null 2>&1 || true
  rprim DEL ha:drill:key >/dev/null 2>&1 || true
  rcli DEL ha:drill:key >/dev/null 2>&1 || true
  if [[ "$KEEP" == 0 ]]; then
    "${COMPOSE[@]}" rm -sf postgres-replica redis-replica >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

step 0 "preflight"
if ! command -v docker >/dev/null 2>&1; then
  echo "drill-ha: docker tidak ditemukan" >&2; exit 1
fi
if docker ps --format '{{.Names}}' | grep -qx "$PG_PRIMARY"; then
  ok "primary $PG_PRIMARY jalan"
else
  bad "primary $PG_PRIMARY tidak jalan (jalankan: make up)"; exit 1
fi

step 1 "prasyarat primary (pg_hba replikasi + slot WAL)"
# `host replication ... 127.0.0.1/32` saja tidak cukup: replika datang dari
# container lain. Tambahkan aturan idempoten lalu reload konfigurasi.
docker exec "$PG_PRIMARY" sh -c "grep -qE '^host +replication +all +all' /var/lib/postgresql/data/pg_hba.conf || echo 'host replication all all scram-sha-256' >> /var/lib/postgresql/data/pg_hba.conf"
pgq "$PG_PRIMARY" "SELECT pg_reload_conf();" >/dev/null 2>&1 && ok "pg_hba dimuat ulang" || bad "pg_reload_conf gagal"
if [[ "$(pgq "$PG_PRIMARY" "SELECT 1 FROM pg_replication_slots WHERE slot_name='${SLOT}';" | tr -d '[:space:]')" == "1" ]]; then
  ok "slot ${SLOT} sudah ada"
else
  pgq "$PG_PRIMARY" "SELECT pg_create_physical_replication_slot('${SLOT}');" >/dev/null 2>&1 \
    && ok "slot ${SLOT} dibuat" || bad "slot ${SLOT} gagal dibuat"
fi

step 2 "naikkan replika (deployments/docker-compose.ha.yml)"
if "${COMPOSE[@]}" up -d postgres-replica redis-replica >/dev/null 2>&1; then
  ok "container replika dinaikkan"
else
  bad "gagal menaikkan container replika"
fi

step 3 "tunggu streaming aktif (base backup bisa butuh puluhan detik)"
state=""; receiver=""
for _ in $(seq 1 60); do
  state="$(pgq "$PG_PRIMARY" "SELECT COALESCE(state,'') FROM pg_stat_replication LIMIT 1;" | tr -d '[:space:]')"
  receiver="$(pgq "$PG_REPLICA" "SELECT COALESCE(status,'') FROM pg_stat_wal_receiver LIMIT 1;" | tr -d '[:space:]')"
  [[ "$state" == "streaming" && "$receiver" == "streaming" ]] && break
  sleep 2
done
[[ "$state" == "streaming" ]] && ok "primary melihat standby state=streaming" || bad "primary: state='${state}' (harus streaming)"
[[ "$receiver" == "streaming" ]] && ok "replika wal receiver status=streaming" || bad "replika: wal receiver='${receiver}'"

step 4 "isolasi & propagasi PostgreSQL"
if pgq "$PG_PRIMARY" "CREATE TABLE IF NOT EXISTS ${MARKER_TABLE} (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, note TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());" >/dev/null 2>&1; then
  ok "tabel marker disiapkan di primary"
else
  bad "gagal menyiapkan tabel marker di primary"
fi

marker="drill-$(date -u +%s)"
pgq "$PG_PRIMARY" "INSERT INTO ${MARKER_TABLE} (note) VALUES ('${marker}');" >/dev/null 2>&1 \
  && ok "INSERT di primary (${marker})" || bad "INSERT di primary gagal"

seen=""
for _ in $(seq 1 30); do
  seen="$(pgq "$PG_REPLICA" "SELECT count(*) FROM ${MARKER_TABLE} WHERE note='${marker}';" | tr -d '[:space:]')"
  [[ "$seen" == "1" ]] && break
  sleep 1
done
[[ "$seen" == "1" ]] && ok "baris terpropagasi primary → replika (streaming WAL bekerja)" \
  || bad "baris tidak muncul di replika (count='${seen}')"

# Standby WAJIB menolak tulis langsung (kalau tidak, replika bisa divergen).
if out="$(pgq "$PG_REPLICA" "INSERT INTO ${MARKER_TABLE} (note) VALUES ('direct-write');" 2>&1)"; then
  bad "replika MENERIMA tulis langsung (seharusnya ditolak)"
else
  if grep -qiE 'read-only|cannot execute' <<<"$out"; then
    ok "replika menolak tulis langsung (read-only standby)"
  else
    bad "replika menolak dengan error tak terduga: $(head -c 140 <<<"$out")"
  fi
fi

[[ "$(pgq "$PG_REPLICA" 'SELECT pg_is_in_recovery();' | tr -d '[:space:]')" == "t" ]] \
  && ok "replika dalam mode recovery (standby)" || bad "replika bukan standby"
[[ "$(pgq "$PG_PRIMARY" "SELECT active FROM pg_replication_slots WHERE slot_name='${SLOT}';" | tr -d '[:space:]')" == "t" ]] \
  && ok "slot ${SLOT} aktif" || bad "slot ${SLOT} tidak aktif"

lag="$(pgq "$PG_PRIMARY" "SELECT COALESCE(pg_wal_lsn_diff(sent_lsn, replay_lsn)::bigint, 0) FROM pg_stat_replication;" | tr -d '[:space:]')"
if [[ "${lag:-x}" =~ ^[0-9]+$ ]] && [[ "${lag:-999999999}" -le 1048576 ]]; then
  ok "lag replikasi ${lag} byte (<= 1 MiB)"
else
  bad "lag replikasi tidak terukur / terlalu besar: '${lag}'"
fi

step 5 "replika Redis (jalur failover state live)"
role="$(rcli INFO replication | grep -E '^role:' | tr -d '\r')"
link="$(rcli INFO replication | grep -E '^master_link_status:' | tr -d '\r')"
[[ "$role" == 'role:slave' ]] && ok "replika Redis role=slave" || bad "replika Redis role='${role}'"
[[ "$link" == 'master_link_status:up' ]] && ok "link replika Redis ke primary up" || bad "link replika Redis: '${link}'"

rprim SET ha:drill:key "$marker" >/dev/null 2>&1 || true
got=""
for _ in $(seq 1 20); do
  got="$(rcli GET ha:drill:key)"
  [[ "$got" == "$marker" ]] && break
  sleep 1
done
[[ "$got" == "$marker" ]] && ok "nilai terpropagasi primary → replika Redis" || bad "propagasi Redis tidak terlihat (got='${got}')"

if [[ "$QUICK" == 1 ]]; then
  step 6 "drill failover Redis — DILEWATI (--quick)"
else
  step 6 "drill failover Redis (promote → tulis → fail-back resync)"
  if "$ROOT/scripts/replication/promote-redis-replica.sh" promote --yes >/dev/null 2>&1; then
    ok "replika dipromosikan menjadi primary"
  else
    bad "promote replika Redis gagal"
  fi
  if rcli SET ha:drill:after-promote ok >/dev/null 2>&1 && [[ "$(rcli GET ha:drill:after-promote)" == "ok" ]]; then
    ok "setelah promote replika menerima tulis (jalur failover hidup)"
  else
    bad "setelah promote replika masih menolak tulis"
  fi
  if "$ROOT/scripts/replication/promote-redis-replica.sh" failback --yes >/dev/null 2>&1; then
    ok "fail-back ke primary asli (master_link_status=up)"
  else
    bad "fail-back gagal (link belum up)"
  fi
  rprim SET ha:drill:key2 after-failback >/dev/null 2>&1 || true
  got2=""
  for _ in $(seq 1 20); do
    got2="$(rcli GET ha:drill:key2)"
    [[ "$got2" == "after-failback" ]] && break
    sleep 1
  done
  [[ "$got2" == "after-failback" ]] && ok "propagasi normal kembali setelah fail-back" \
    || bad "propagasi tidak pulih setelah fail-back (got='${got2}')"
  rprim DEL ha:drill:key2 >/dev/null 2>&1 || true
fi

echo
echo "===== HA SUMMARY pass=${pass} fail=${fail} ====="
if [[ "$fail" -gt 0 ]]; then
  echo 'HA-DRILL: FAILED' >&2
  exit 1
fi
echo 'HA-DRILL: ALL PASS'
