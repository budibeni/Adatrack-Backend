#!/usr/bin/env bash
# ============================================================================
# loadtest-suite.sh — Load test GAP #6 (Phase B4).
#
# Skenario (urutan):
#   1. baseline  : 400 msg/s  × 5 menit  (target operasional multi-tenant)
#   2. peak      : 2000 msg/s × 1 menit  (beban puncak PRD lama; burst)
#   3. endurance : 400 msg/s  × ENDURANCE_DURATION (default 24 jam)
#
# Prasyarat: infra compose UP + keenam service berjalan (lihat PANDUAN.md).
# Verifikasi data loss dilakukan via delta COUNT(telemetry_logs) sebelum/
# sesudah tiap skenario (hasil ditulis ke LOADTEST_REPORT).
# ============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"

# load_env membaca file .env per-baris (bukan `source`) agar nilai yang memuat
# karakter shell (mis. JWT_SECRET=...jwt|06) tidak dieksekusi sebagai pipeline.
load_env() {
  local f="$1" k v
  [ -f "$f" ] || return 0
  while IFS='=' read -r k v; do
    k="${k%"${k##*[![:space:]]}"}"   # trim trailing space
    k="${k#"${k%%[![:space:]]*}"}"   # trim leading space
    case "$k" in ''|\#*) continue;; esac
    export "$k=$v"
  done < <(grep -vE '^[[:space:]]*(#|$)' "$f")
}
load_env "$ENV_FILE"

ENDURANCE_DURATION="${ENDURANCE_DURATION:-24h}"
REPORT="${REPORT:-/tmp/loadtest_report_$(date +%Y%m%d_%H%M%S).txt}"
LT="$SCRIPT_DIR/../loadtest/loadtest"

TENANT_DB="${TENANT_DB:-dev001}"
count_rows() {
  # Dialect-aware (PRD §7.1.1): postgres = schema adatrack_gps_<tenant> di
  # $POSTGRES_DB; mysql = database. Tenant seed = dev001 (IMEI loadtest).
  if [ "${DATABASE_PROVIDER:-mysql}" = "postgres" ]; then
    docker exec postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tAc \
      "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null | tr -d '[:space:]'
  else
    docker exec mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N \
      -e "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null
  fi
}

run_scenario() {
  local name="$1" devices="$2" rate="$3" dur="$4"
  echo "===== $name: $devices device × $rate msg/s × $dur =====" | tee -a "$REPORT"
  local before after sent
  before=$(count_rows)
  "$LT" -devices "$devices" -rate "$rate" -duration "$dur" 2>&1 | tee /tmp/lt_last.txt
  sent=$(grep -oP 'Total frame terkirim: \K[0-9]+' /tmp/lt_last.txt | tail -1)
  sleep 12 # beri worker-persistence flush batch terakhir (5s) + margin
  after=$(count_rows)
  local delta=$(( after - before ))
  local loss=$(( sent - delta ))
  echo "RESULT $name: sent=$sent delta_mysql=$delta loss=$loss" | tee -a "$REPORT"
}

{
  echo "Load test suite — $(date -Is)"
  run_scenario "baseline_400msg_s_5m" 20 20 5m
  run_scenario "peak_2000msg_s_1m"   100 20 1m
  run_scenario "endurance_400msg_s_${ENDURANCE_DURATION}" 20 20 "$ENDURANCE_DURATION"
} 2>&1 | tee -a "$REPORT"

echo "Laporan lengkap: $REPORT"
