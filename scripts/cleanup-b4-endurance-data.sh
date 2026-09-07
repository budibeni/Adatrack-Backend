#!/usr/bin/env bash
# ============================================================================
# cleanup-b4-endurance-data.sh — Hapus SEMUA data test B4 endurance 24 jam.
#
# DESTRUKTIF — tidak bisa di-undo. Hanya boleh dijalankan SETELAH chunked
# endurance selesai (state STATUS=completed) atau dengan --force.
#
# Menghapus:
#   - Postgres tenant dev001: telemetry_logs, fuel_logs, alerts, notifications
#     (seluruh row = artefak synthetic endurance/loadtest B4; baseline seed
#     24 rb row sebelumnya juga data test).
#   - Redis: semua key `adatrack_gps:dev001:*` (live state, fuel/geofence/alert state).
#   - NATS JetStream: purge seluruh pesan pada semua stream (via cmd/nats-purge).
#   - State dir ~/b4_chunked_endurance (log + checkpoint endurance).
#
# TIDAK menghapus: vehicle/geofence/route/speed_configs/fuel_configs/media_config
# (konfigurasi & seed), data tenant lain, MinIO objek, container/infra.
#
# Pemakaian:
#   ./cleanup-b4-endurance-data.sh            # normal (guard endurance selesai)
#   ./cleanup-b4-endurance-data.sh --force    # paksa (endurance belum selesai)
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$BACKEND_DIR/.env"
STATE_DIR="$HOME/b4_chunked_endurance"
STATE_FILE="$STATE_DIR/state.txt"
TENANT_DB="${TENANT_DB:-dev001}"
SCHEMA="adatrack_gps_${TENANT_DB}"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    *) echo "Flag tidak dikenal: $arg (pakai --force)" >&2; exit 1 ;;
  esac
done

load_env() { # baca .env per-baris (aman untuk nilai ber-`|`)
  local f="$1" k v
  [ -f "$f" ] || return 0
  while IFS='=' read -r k v; do
    k="${k%"${k##*[![:space:]]}"}"; k="${k#"${k%%[![:space:]]*}"}"
    case "$k" in ''|\#*) continue;; esac
    export "$k=$v"
  done < <(grep -vE '^[[:space:]]*(#|$)' "$f")
}
load_env "$ENV_FILE"

PG="${PG:-docker exec postgres psql -U ${POSTGRES_USER:-adatrack_gps_user} -d ${POSTGRES_DB:-adatrack_gps_db}}"
REDIS="docker exec redis redis-cli"

pg_num() { # count rows di tabel tenant dev001
  $PG -tAc "SELECT count(*) FROM ${SCHEMA}.$1;" | tr -d '[:space:]'
}

echo "================================================"
echo "  CLEANUP DATA TEST B4 (endurance 24h)"
echo "  Tenant: $TENANT_DB  |  Force: $FORCE"
echo "================================================"

# --- GUARD: jangan jalan selagi endurance belum selesai --------------------
if pgrep -f "loadtest/loadtest" >/dev/null 2>&1 || pgrep -f "run-endurance-chunked.sh" >/dev/null 2>&1; then
  if [ "$FORCE" -eq 0 ]; then
    echo "ABORT: endurance MASIH BERJALAN (loadtest/run-endurance-chunked aktif)."
    echo "       Jalankan setelah STATUS=completed, atau pakai --force."
    exit 1
  fi
  echo "WARN: --force — endurance masih berjalan, cleanup diteruskan."
fi
if [ -f "$STATE_FILE" ] && ! grep -q "STATUS=completed" "$STATE_FILE"; then
  if [ "$FORCE" -eq 0 ]; then
    echo "ABORT: state endurance ($STATE_FILE) belum STATUS=completed."
    exit 1
  fi
  echo "WARN: --force — state belum completed, cleanup diteruskan."
fi
# --- BEFORE ---------------------------------------------------------------
T_BEFORE=$(pg_num telemetry_logs)
F_BEFORE=$(pg_num fuel_logs)
A_BEFORE=$(pg_num alerts)
N_BEFORE=$(pg_num notifications)
R_BEFORE=$($REDIS --scan --pattern "adatrack_gps:${TENANT_DB}:*" | wc -l | tr -d '[:space:]')
JS_BEFORE=$(docker exec nats wget -qO- http://localhost:8222/jsz 2>/dev/null | grep -oE '"messages": [0-9]+' | head -1 | grep -oE '[0-9]+' || echo 0)
echo "  BEFORE: telemetry_logs=$T_BEFORE fuel_logs=$F_BEFORE alerts=$A_BEFORE"
echo "          notifications=$N_BEFORE | redis_keys=$R_BEFORE | nats_msgs=$JS_BEFORE"
echo "------------------------------------------------"

# --- 1/4 Postgres: truncate tabel artefak test ----------------------------
echo ">> [1/4] TRUNCATE ${SCHEMA}.{telemetry_logs, fuel_logs, alerts, notifications} ..."
if ! $PG -v ON_ERROR_STOP=1 -c "TRUNCATE TABLE ${SCHEMA}.telemetry_logs, ${SCHEMA}.fuel_logs, ${SCHEMA}.alerts, ${SCHEMA}.notifications;" ; then
  echo "ERROR: TRUNCATE gagal — hentikan cleanup." >&2
  exit 1
fi
echo "   OK."

# --- 2/4 Redis: hapus semua key tenant dev001 ------------------------------
echo ">> [2/4] REDIS: hapus key 'adatrack_gps:${TENANT_DB}:*' ..."
$REDIS --scan --pattern "adatrack_gps:${TENANT_DB}:*" \
  | xargs -r -n50 $REDIS DEL >/dev/null 2>&1 || true
echo "   OK."

# --- 3/4 NATS: purge semua stream JetStream ---------------------------------
echo ">> [3/4] NATS: purge semua stream JetStream (cmd/nats-purge) ..."
NATS_PURGE_DIR="$BACKEND_DIR/cmd/nats-purge"
NATS_PURGE_BIN="$NATS_PURGE_DIR/nats-purge"
if [ ! -x "$NATS_PURGE_BIN" ]; then
  echo "   build nats-purge ..."
  (cd "$NATS_PURGE_DIR" && GOFLAGS=-mod=mod go build -o nats-purge .) || {
    echo "WARN: build nats-purge gagal — NATS TIDAK di-purge (retensi 48h akan membersihkan pelan-pelan)." >&2
    NATS_PURGE_BIN=""
  }
fi
if [ -n "$NATS_PURGE_BIN" ]; then
  NATS_URL="${NATS_URL:-127.0.0.1:4222}" "$NATS_PURGE_BIN" \
    || echo "WARN: purge NATS sebagian gagal (lihat output tool)."
fi

# --- 4/4 State endurance -----------------------------------------------------
echo ">> [4/4] Hapus state endurance ($STATE_DIR) ..."
rm -rf "$STATE_DIR" 2>/dev/null || true
echo "   OK."

# --- AFTER -----------------------------------------------------------------
T_AFTER=$(pg_num telemetry_logs)
F_AFTER=$(pg_num fuel_logs)
A_AFTER=$(pg_num alerts)
N_AFTER=$(pg_num notifications)
R_AFTER=$($REDIS --scan --pattern "adatrack_gps:${TENANT_DB}:*" | wc -l | tr -d '[:space:]')
JS_AFTER=$(docker exec nats wget -qO- http://localhost:8222/jsz 2>/dev/null | grep -oE '"messages": [0-9]+' | head -1 | grep -oE '[0-9]+' || echo 0)
echo "------------------------------------------------"
echo "  AFTER : telemetry_logs=$T_AFTER fuel_logs=$F_AFTER alerts=$A_AFTER"
echo "          notifications=$N_AFTER | redis_keys=$R_AFTER | nats_msgs=$JS_AFTER"
echo "================================================"
echo "SELESAI — semua data test B4 endurance telah dihapus."