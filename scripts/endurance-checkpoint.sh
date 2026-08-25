#!/usr/bin/env bash
# ============================================================================
# endurance-checkpoint.sh — Pelindung hasil endurance 24 jam (pelajaran dari
# crash WSL 2026-08-24 yang menghapus /tmp dan memutus run sebelum selesai).
#
# Dipanggil SEBELUM/SAMA dengan menjalankan loadtest:
#   ./endurance-checkpoint.sh <pid_loadtest> <baseline_rows> [log_loadtest]
#
# Setiap 5 menit mencatat satu baris ke ~/endurance_checkpoint.log:
#   <timestamp> <msgs_terkirim_dari_log> <rows_mysql> <delta>
# sehingga walau mesin crash, kurva sent-vs-inserted sampai detik terakhir
# tetap terdokumentasi di luar /tmp.
# ============================================================================
set -uo pipefail

PID_LT="${1:?pakai: $0 <pid_loadtest> <baseline_rows> [log]}"
BASELINE="${2:?baseline rows wajib}"
LOG="${3:-/tmp/b4_endurance24.log}"
OUT="$HOME/endurance_checkpoint.log"
ENV_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../.env"

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
load_env "$ENV_FILE"

last_msgs() {
  grep -oP '\[\s*progress\] \K[0-9]+' "$LOG" 2>/dev/null | tail -1 || echo 0
}

db_rows() {
  load_env "$ENV_FILE"
  # Sadar-dialek (PRD §7.1.1): postgres = schema adatrack_gps_<tenant>, mysql = database.
  TENANT_DB="${TENANT_DB:-dev001}"
  if [ "${DATABASE_PROVIDER:-mysql}" = "postgres" ]; then
    docker exec postgres psql -U "${POSTGRES_USER:-adatrack_gps_user}" -d "${POSTGRES_DB:-adatrack_gps_db}" -tAc \
      "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null || echo "?"
  else
    docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:-}" -N \
      -e "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null || echo "?"
  fi
}

echo "checkpoint mulai $(date -Is) pid=$PID_LT baseline=$BASELINE out=$OUT" >> "$OUT"

while kill -0 "$PID_LT" 2>/dev/null; do
  M=$(last_msgs)
  R=$(db_rows)
  D=$(( ${R:-0} > 0 ? R - BASELINE : 0 ))
  echo "$(date -Is) msgs=$M rows=$R delta=$D" >> "$OUT"
  sleep 300
done

# Entri penutup saat loadtest berhenti (selesai ATAU mati).
M=$(last_msgs); R=$(mysql_rows)
echo "$(date -Is) END msgs=$M rows=$R delta=$(( ${R:-0} - BASELINE ))" >> "$OUT"
echo "checkpoint selesai — lihat $OUT"
