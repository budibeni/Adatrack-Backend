#!/usr/bin/env bash
# ============================================================================
# drill-redis-failover.sh — Latihan failover Redis sesuai
# docs/HIGH_AVAILABILITY.md §5 langkah 4 + fail-back (alur terbalik).
#
# Mengikuti dokumen HA secara harfiah:
#   [0] pra-cek topologi (primary=master, replica=slave, link up)
#   [1] probe tulis di PRIMARY → wajib terbaca di REPLICA
#   [2] simulasi server A mati (stop container redis)
#   [3] promote REPLICA → master via promote-redis-replica.sh (§5 #4)
#   [4] verifikasi data survive di node terpromosi (state ≤5 mnt, §3)
#   [5] "A pulih" — start ulang container redis
#   [6] fail-back: replika kembali mengikuti primary (REPLICAOF), tunggu sync
#
# Aman untuk dev/rehearsal 1-host: tidak ada trafik aplikasi saat drill;
# dataset kecil sehingga resync penuh berlangsung detik.
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC2092
PRIMARY="${REDIS_PRIMARY_CONTAINER:-redis}"
REPLICA="${REDIS_REPLICA_CONTAINER:-redis_replica}"
PORT_HOST="$(grep -E '^[[:space:]]*REDIS_PORT[[:space:]]*=' "${ENV_FILE:-$(dirname "$SCRIPT_DIR")/.env}" 2>/dev/null | tail -n1 | sed -E 's/^[^=]*=[[:space:]]*//' | tr -dc '0-9')"
PORT_HOST="${PORT_HOST:-6390}"
PROBE_KEY="ha:drill:probe"

step() { printf '\n== %s ==\n' "$*"; }

info() { docker exec "$1" redis-cli INFO replication 2>/dev/null \
           | grep -E '^(role|master_host|master_link_status):' | tr -d '\r'; }

step "[0/6] Pra-cek topologi"
echo "--- primary (${PRIMARY}) ---"; info "$PRIMARY"
echo "--- replica (${REPLICA}) ---"; info "$REPLICA"

step "[1/6] Probe tulis di PRIMARY → wajib terbaca di REPLICA"
TS="$(date +%s)"
docker exec "$PRIMARY" redis-cli SET "$PROBE_KEY" "drill-$TS" EX 600 >/dev/null
sleep 1
V1="$(docker exec "$REPLICA" redis-cli GET "$PROBE_KEY" | tr -d '\r')"
echo "replica membaca: $V1"
if [ "$V1" != "drill-$TS" ]; then echo "DRILL_ABORT: replika belum sinkron"; exit 1; fi

step "[2/6] Simulasi A mati → stop container ${PRIMARY}"
docker stop "$PRIMARY" >/dev/null
echo "${PRIMARY} stopped"

step "[3/6] PROMOTE replika → master (runbook §5 #4)"
printf 'PROMOTE\n' | "$SCRIPT_DIR/promote-redis-replica.sh"

step "[4/6] Verifikasi data survive di node terpromosi"
V2="$(docker exec "$REPLICA" redis-cli GET "$PROBE_KEY" | tr -d '\r')"
if [ "$V2" = "drill-$TS" ]; then
  echo "DATA_SURVIVED ($V2)"
else
  echo "DRILL_FAIL: data hilang saat promote ($V2)"; exit 1
fi

step "[5/6] 'A pulih' — start ulang ${PRIMARY}"
docker start "$PRIMARY" >/dev/null

step "[6/6] FAIL-BACK: replika kembali mengikuti PRIMARY (§5, alur terbalik)"
docker exec "$REPLICA" redis-cli REPLICAOF host.docker.internal "$PORT_HOST" >/dev/null
ROLE=""; LINK=""
for _ in $(seq 1 30); do
  ROLE="$(docker exec "$REPLICA" redis-cli INFO replication 2>/dev/null | grep '^role:' | cut -d: -f2 | tr -d '\r')"
  LINK="$(docker exec "$REPLICA" redis-cli INFO replication 2>/dev/null | grep '^master_link_status:' | cut -d: -f2 | tr -d '\r')"
  [ "$ROLE" = "slave" ] && [ "$LINK" = "up" ] && break
  sleep 2
done
echo "replica role=$ROLE master_link_status=$LINK"
if [ "$ROLE" = "slave" ] && [ "$LINK" = "up" ]; then
  V3="$(docker exec "$REPLICA" redis-cli GET "$PROBE_KEY" | tr -d '\r')"
  [ "$V3" = "drill-$TS" ] && echo "RESYNC_DATA_OK ($V3)" || echo "catatan: nilai probe belum terlihat (akan ikut dataset primary)"
  echo
  echo "DRILL_OK — failover + fail-back sesuai HIGH_AVAILABILITY.md §5 selesai."
else
  echo "DRILL_INCOMPLETE — cek replication-status.sh"; exit 1
fi