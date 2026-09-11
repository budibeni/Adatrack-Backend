#!/usr/bin/env bash
# ============================================================================
# cleanup-mysql-drill-data.sh — Hapus data hasil drill replikasi + endurance
# MySQL (B4, 2026-09-11).
#
# DESTRUKTIF — tidak bisa di-undo. Jalankan HANYA setelah drill/loadtest
# selesai (loadtest tidak sedang berjalan), atau dengan --force.
#
# Menghapus (tenant default: DEF001 → DB adatrack_gps_def001):
#   - MySQL PRIMARY : TRUNCATE tabel artefak test (telemetry_logs, fuel_logs,
#     alerts, notifications). GTID mereplikasi TRUNCATE ke replica — AFTER
#     memverifikasi count=0 di KEDUA server.
#   - MySQL PRIMARY : tabel probe repl_drill_probe (bukti propagasi drill).
#   - Redis         : semua key `adatrack_gps:def001:*` (live state,
#     fuel/geofence/alert state).
#   - NATS JetStream: purge seluruh pesan pada semua stream (cmd/nats-purge).
#   - File sisa drill: dump replikasi /tmp/repl_seed.sql.gz (di host) + report
#     /tmp/loadtest_report_*.txt + state ~/mysql_drill (log ~/b4_endurance
#     TIDAK dihapus).
#
# TIDAK menghapus: companies/vehicles/users/vehicle_imei_map (seed), konfigurasi
# (speed_configs, fuel_configs, media config, dst.), volume/container.
#
# Pemakaian:
#   ./cleanup-mysql-drill-data.sh            # normal (guard loadtest aktif)
#   ./cleanup-mysql-drill-data.sh --force    # paksa
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$BACKEND_DIR/.env"
TENANT_DB="${TENANT_DB:-def001}"   # tenant seed drill = DEF001 (bukan dev001)
DB="adatrack_gps_${TENANT_DB}"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    *) echo "Flag tidak dikenal: $arg (pakai --force)" >&2; exit 1 ;;
  esac
done

load_env() { # baca .env per-baris (aman utk nilai ber-`|`)
  local f="$1" k v
  [ -f "$f" ] || return 0
  while IFS='=' read -r k v; do
    k="${k%"${k##*[![:space:]]}"}"; k="${k#"${k%%[![:space:]]*}"}"
    case "$k" in ''|\#*) continue;; esac
    export "$k=$v"
  done < <(grep -vE '^[[:space:]]*(#|$)' "$f")
}
load_env "$ENV_FILE"

# GUARD: jangan jalan selagi loadtest/drill masih berjalan.
if pgrep -f "loadtest/loadtest" >/dev/null 2>&1 || pgrep -f "loadtest-suite.sh" >/dev/null 2>&1; then
  if [ "$FORCE" -eq 0 ]; then
    echo "ABORT: loadtest/drill MASIH BERJALAN. Tunggu selesai, atau pakai --force."
    exit 1
  fi
  echo "WARN: --force — loadtest masih berjalan, cleanup diteruskan."
fi

MY() { # MY <db> <flags?> — SQL di-PIPE via stdin di PRIMARY via socket
  local db="${1:-}"; shift
  docker exec -i mysql sh -c "mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" --protocol=socket -hlocalhost $db $*"
}
MYR() { # sama, di REPLICA (verifikasi AFTER) — SQL via stdin
  local db="${1:-}"; shift
  docker exec -i mysql_replica sh -c "mysql -uroot -p\"\$MYSQL_ROOT_PASSWORD\" $db $*"
}

echo "================================================"
echo "  CLEANUP DATA DRILL REPLIKASI + ENDURANCE MySQL"
echo "  Tenant: $TENANT_DB ($DB)  |  Force: $FORCE"
echo "================================================"

pg_num() { MY "$DB" -N <<< "SELECT COUNT(*) FROM $1;" 2>/dev/null | tr -d '[:space:]'; }
pg_num_replica() { MYR "$DB" -N <<< "SELECT COUNT(*) FROM $1;" 2>/dev/null | tr -d '[:space:]'; }
redis_count() { docker exec redis redis-cli --scan --pattern "adatrack_gps:${TENANT_DB}:*" 2>/dev/null | wc -l | tr -d '[:space:]'; }
nats_count() { docker exec nats wget -qO- http://localhost:8222/jsz 2>/dev/null | grep -oE '"messages": [0-9]+' | head -1 | grep -oE '[0-9]+' || echo 0; }

# --- BEFORE -----------------------------------------------------------------
T_BEFORE=$(pg_num telemetry_logs); F_BEFORE=$(pg_num fuel_logs)
A_BEFORE=$(pg_num alerts); N_BEFORE=$(pg_num notifications)
R_BEFORE=$(redis_count); JS_BEFORE=$(nats_count)
echo "  BEFORE (primary): telemetry_logs=$T_BEFORE fuel_logs=$F_BEFORE alerts=$A_BEFORE"
echo "           notifications=$N_BEFORE | redis_keys=$R_BEFORE | nats_msgs=$JS_BEFORE"
echo "           replica.telemetry_logs=$(pg_num_replica telemetry_logs)"
echo "------------------------------------------------"

# --- 1/6 MySQL: TRUNCATE tabel artefak test (mereplikasi ke replica) --------
echo ">> [1/6] TRUNCATE $DB.{telemetry_logs, fuel_logs, alerts, notifications} ..."
if ! echo "TRUNCATE TABLE telemetry_logs; TRUNCATE TABLE fuel_logs; TRUNCATE TABLE alerts; TRUNCATE TABLE notifications;" | MY "$DB"; then
  echo "ERROR: TRUNCATE gagal — hentikan cleanup." >&2
  exit 1
fi
echo "   OK (GTID mereplikasi truncate ke replica)."

# --- 2/6 MySQL: drop tabel probe propagasi drill ----------------------------
echo ">> [2/6] DROP TABLE adatrack_gps_master.repl_drill_probe (bukti propagasi) ..."
echo "DROP TABLE IF EXISTS repl_drill_probe;" | MY adatrack_gps_master || echo "WARN: drop probe gagal (lanjut)."
echo "   OK."

# --- 3/6 Redis: hapus semua key tenant --------------------------------------
echo ">> [3/6] REDIS: hapus key 'adatrack_gps:${TENANT_DB}:*' ..."
docker exec redis redis-cli --scan --pattern "adatrack_gps:${TENANT_DB}:*" \
  | xargs -r -n50 docker exec -i redis redis-cli DEL >/dev/null 2>&1 || true
echo "   OK."

# --- 4/6 NATS: purge semua stream JetStream ---------------------------------
echo ">> [4/6] NATS: purge semua stream JetStream (cmd/nats-purge) ..."
NATS_PURGE_DIR="$BACKEND_DIR/cmd/nats-purge"
NATS_PURGE_BIN="$NATS_PURGE_DIR/nats-purge"
if [ ! -x "$NATS_PURGE_BIN" ]; then
  echo "   build nats-purge ..."
  (cd "$NATS_PURGE_DIR" && GOFLAGS=-mod=mod go build -o nats-purge .) || {
    echo "WARN: build nats-purge gagal — NATS TIDAK di-purge (retensi 48h akan membersihkan)." >&2
    NATS_PURGE_BIN=""
  }
fi
if [ -n "$NATS_PURGE_BIN" ]; then
  NATS_URL="${NATS_URL:-127.0.0.1:4222}" "$NATS_PURGE_BIN" \
    || echo "WARN: purge NATS sebagian gagal (lihat output tool)."
fi

# --- 5/6 File sisa drill ------------------------------------------------------
echo ">> [5/6] Hapus file sisa drill (dump replikasi + report + state) ..."
rm -f /tmp/repl_seed.sql.gz 2>/dev/null || true
rm -f /tmp/loadtest_report_*.txt /tmp/lt_last.txt 2>/dev/null || true
rm -rf "$HOME/mysql_drill" 2>/dev/null || true
echo "   OK."

# --- AFTER ------------------------------------------------------------------
sleep 3 # beri relay replica waktu (GTID streaming; biasanya instan)
T_AFTER=$(pg_num telemetry_logs); F_AFTER=$(pg_num fuel_logs)
A_AFTER=$(pg_num alerts); N_AFTER=$(pg_num notifications)
T_REPLICA=$(pg_num_replica telemetry_logs)
R_AFTER=$(redis_count); JS_AFTER=$(nats_count)
echo "------------------------------------------------"
echo "  AFTER (primary): telemetry_logs=$T_AFTER fuel_logs=$F_AFTER alerts=$A_AFTER"
echo "           notifications=$N_AFTER | redis_keys=$R_AFTER | nats_msgs=$JS_AFTER"
echo "           replica.telemetry_logs=$T_REPLICA (harus 0 — propagate truncate)"
echo "================================================"
echo "SELESAI — data drill/endurance MySQL telah dihapus (seed & konfigurasi utuh)."
