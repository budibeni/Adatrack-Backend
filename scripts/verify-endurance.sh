#!/usr/bin/env bash
# ============================================================================
# verify-endurance.sh — Verifikasi hasil endurance 24 jam (GAP #6).
#
# Bandingkan jumlah frame terkirim (ringkasan loadtest) vs delta baris MySQL.
# Prasyarat: file baseline /tmp/b4_endurance24_start.txt (dibuat saat start).
#
# Pakai: ./verify-endurance.sh [log_loadtest] [baseline_file]
# ============================================================================
set -euo pipefail
LOG="${1:-/tmp/b4_endurance24.log}"
BASELINE_FILE="${2:-/tmp/b4_endurance24_start.txt}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# load_env membaca file .env per-baris (bukan `source`) agar nilai ber-`|`
# (mis. JWT_SECRET) tidak dieksekusi sebagai pipeline saat sourcing.
load_env() {
  local f="$1" k v
  [ -f "$f" ] || return 0
  while IFS='=' read -r k v; do
    k="${k%"${k##*[![:space:]]}"}"
    k="${k#"${k%%[![:space:]]*}"}"
    case "$k" in ''|\#*) continue;; esac
    export "$k=$v"
  done < <(grep -vE '^[[:space:]]*(#|$)' "$f")
}
load_env "$SCRIPT_DIR/../.env"

[ -f "$BASELINE_FILE" ] || { echo "baseline tidak ada: $BASELINE_FILE"; exit 1; }
BEFORE=$(cat "$BASELINE_FILE")

SENT=$(grep -oP 'Total frame terkirim: \K[0-9]+' "$LOG" | tail -1 || true)
if [ -z "$SENT" ]; then
  echo "Loadtest belum selesai (ringkasan belum ada di $LOG)."
  tail -1 "$LOG"
  exit 0
fi

# Sadar-dialek (PRD §7.1.1): postgres = schema adatrack_gps_<tenant> di $POSTGRES_DB,
# mysql = database adatrack_gps_<tenant>. Tenant default dev001 (seed IMEI loadtest).
TENANT_DB="${TENANT_DB:-dev001}"
if [ "${DATABASE_PROVIDER:-mysql}" = "postgres" ]; then
  AFTER=$(docker exec postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
    "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null | tr -d '[:space:]')
else
  AFTER=$(docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N \
    -e "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null)
fi

DELTA=$(( AFTER - BEFORE ))
LOSS=$(( SENT - DELTA ))

echo "================ HASIL ENDURANCE ================"
echo "Durasi        : $(grep -oP 'Durasi: \K[0-9.]+' "$LOG" | tail -1) s"
echo "Terkirim      : $SENT"
echo "Δ MySQL       : $DELTA  ($BEFORE → $AFTER)"
echo "Data Loss     : $LOSS"
[ "$LOSS" -eq 0 ] && echo "STATUS        : PASS (0 data loss)" || echo "STATUS        : FAIL (${LOSS} baris hilang)"
echo "================================================="
