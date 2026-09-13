#!/usr/bin/env bash
# ============================================================================
# run-endurance-crashed-safe.sh — B4 endurance 24h runner yang tahan crash WSL.
#
# Perbedaan dari loadtest-suite.sh asli:
#   1. Dipanggil via nohup+disown → bertahan melewati disconnect WSL.
#   2. Baseline+peak DAN endurance dijalankan SEBELUM run; jika crash,
#      checkpoint log di ~/endurance_checkpoint.log tetap utuh.
#   3. Loadtest connect ke TCP_PORT (bukan 9000 default) via -host flag.
#   4. Semua output (termasuk progress) ditulis ke log yang sama.
#
# Usage: ./run-endurance-crashed-safe.sh
# ============================================================================
set -uo pipefail

BACKEND_DIR="/home/user/projects/ajb_gps/backend"
ENV_FILE="$BACKEND_DIR/.env"
LOG_DIR="$HOME/b4_endurance/logs"
mkdir -p "$LOG_DIR"

# load_env: baca .env per-baris (bukan source) agar nilai ber-`|` tidak
# dieksekusi sebagai pipeline.
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

# Override: port 9000 dipakai MinIO di dev ini; ingestion-tcp jalan di 9003.
TCP_PORT="${TCP_PORT_INGESTION:-9003}"
TENANT_DB="${TENANT_DB:-dev001}"
LT="$BACKEND_DIR/loadtest/loadtest"
LOG="$LOG_DIR/b4_endurance24.log"
CHECKPOINT_LOG="$HOME/endurance_checkpoint.log"

# Hitung row count dialect-aware (postgres = schema, mysql = database).
count_rows() {
  if [ "${DATABASE_PROVIDER:-mysql}" = "postgres" ]; then
    docker exec postgres psql -U "${POSTGRES_USER:-adatrack_gps_user}" \
      -d "${POSTGRES_DB:-adatrack_gps_db}" -tAc \
      "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null \
      | tr -d '[:space:]'
  else
    docker exec mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD:-}" -N \
      -e "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null
  fi
}

echo "=== B4 Endurance Runner (crash-safe) ===" | tee -a "$LOG"
echo "TCP port: $TCP_PORT | Tenant: $TENANT_DB | Provider: ${DATABASE_PROVIDER:-mysql}" | tee -a "$LOG"
echo "Start time: $(date -Is)" | tee -a "$LOG"

# 1. Record baseline.
BEFORE=$(count_rows)
echo "BASELINE rows=$BEFORE" | tee -a "$LOG"
echo "$BEFORE" > "$LOG_DIR/b4_endurance24_start.txt"

# 2. Run loadtest for 24h at 400 msg/s (20 devices × 20 msg/s).
#    -host override ke TCP_PORT karena 9000 dipakai MinIO.
#    nohup+disown: tahan crash WSL; output ke log yang sama.
echo "Starting loadtest (24h endurance @ 400 msg/s target $TCP_PORT)..." | tee -a "$LOG"
nohup stdbuf -oL "$LT" \
  -devices 20 -rate 20 -duration 24h \
  -host "127.0.0.1:${TCP_PORT}" \
  > "$LOG" 2>&1 &
LT_PID=$!
echo "loadtest PID=$LT_PID" | tee -a "$LOG"
echo "$LT_PID" > "$LOG_DIR/b4_endurance24_pid.txt"

# 3. Start checkpoint monitor (tiap 5 menit) in background.
if [ "${SKIP_CHECKPOINT:-0}" = "1" ]; then
  echo "SKIP_CHECKPOINT=1 — not starting checkpoint monitor" | tee -a "$LOG"
else
  nohup bash "$BACKEND_DIR/scripts/endurance-checkpoint.sh" "$LT_PID" "$BEFORE" "$LOG" \
    > "$LOG_DIR/checkpoint-monitor.log" 2>&1 &
  CKPT_PID=$!
  echo "checkpoint monitor PID=$CKPT_PID" | tee -a "$LOG"
fi

# 4. Wait for loadtest to finish (atau sampai WSL crash, checkpoint tetap jalan).
echo "Waiting for loadtest PID=$LT_PID to complete..." | tee -a "$LOG"
wait "$LT_PID" 2>/dev/null
LT_EXIT=$?

# 5. Stop checkpoint.
if [ -n "${CKPT_PID:-}" ] && kill -0 "$CKPT_PID" 2>/dev/null; then
  kill "$CKPT_PID" 2>/dev/null
fi

# 6. Verify results.
AFTER=$(count_rows)
echo "=== ENDURANCE RESULT ===" | tee -a "$LOG"
echo "BEFORE: $BEFORE" | tee -a "$LOG"
echo "AFTER:  $AFTER" | tee -a "$LOG"
echo "DELTA:  $(( AFTER - BEFORE ))" | tee -a "$LOG"
# Parse final msgs from log (format: "[progress] N msgs").
SENT=$(grep -a -oP '\[progress\]\s*\K[0-9]+' "$LOG" | tail -1 || echo 0)
echo "SENT:   $SENT" | tee -a "$LOG"
echo "LOSS:   $(( SENT - (AFTER - BEFORE) ))" | tee -a "$LOG"
echo "End time: $(date -Is)" | tee -a "$LOG"

if [ "$(( SENT - (AFTER - BEFORE) ))" -eq 0 ]; then
  echo "STATUS: PASS (0 data loss)" | tee -a "$LOG"
else
  echo "STATUS: FAIL" | tee -a "$LOG"
fi
