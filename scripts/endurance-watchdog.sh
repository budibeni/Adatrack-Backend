#!/usr/bin/env bash
# ============================================================================
# endurance-watchdog.sh — Auto-resume chunked endurance setelah WSL restart.
#
# Skrip ini mendeteksi apakah chunked endurance masih berjalan. Jika tidak
# (misalnya setelah WSL restart), ia otomati menjalankan ulang
# run-endurance-chunked.sh untuk melanjutkan dari chunk terakhir.
#
# Cara pakai:
#   1. Manual:  ./endurance-watchdog.sh
#   2. Windows Task Scheduler: jalankan saat login/WSL start
#      Target: wsl.exe -d <distro> -e bash -c "/path/to/endurance-watchdog.sh"
#
# Env:
#   MAX_RETRIES    max restart per invocation (default: 999999 = unlimited)
#   RETRY_DELAY    detik antar retry (default: 60)
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAX_RETRIES="${MAX_RETRIES:-999999}"
RETRY_DELAY="${RETRY_DELAY:-60}"
RETRY=0

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Endurance watchdog started."

while [ "$RETRY" -lt "$MAX_RETRIES" ]; do
  RETRY=$(( RETRY + 1 ))

  # Cek apakah sudah completed/failed — terminal states, watchdog selesai
  STATE_FILE="$HOME/b4_chunked_endurance/state.txt"
  if [ -f "$STATE_FILE" ] && grep -qE "STATUS=(completed|failed)" "$STATE_FILE"; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Endurance sudah $(grep -oP 'STATUS=\K\w+' "$STATE_FILE" | head -1). Watchdog selesai."
    exit 0
  fi

  # Cek apakah chunked endurance sudah running
  if pgrep -f "run-endurance-chunked.sh" > /dev/null 2>&1; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Chunked endurance sudah running. Menunggu..."
    sleep 300 # cek tiap 5 menit
    continue
  fi

  # Tidak running — jalankan ulang
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] Memulai chunked endurance (attempt #$RETRY)..."
  "$SCRIPT_DIR/run-endurance-chunked.sh" &
  LT_PID=$!
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] PID=$LT_PID"

  # Tunggu selesai atau mati
  wait "$LT_PID" 2>/dev/null
  EXIT=$?

  echo "[$(date '+%Y-%m-%d %H:%M:%S')] Exit code: $EXIT"

  # Jika exit 0 (completed), watchdog selesai
  if [ "$EXIT" -eq 0 ] && [ -f "$STATE_FILE" ] && grep -qE "STATUS=(completed|failed)" "$STATE_FILE"; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Endurance $(grep -oP 'STATUS=\K\w+' "$STATE_FILE" | head -1). Watchdog selesai."
    exit 0
  fi

  # Jika mati karena crash, tunggu sebentar lalu restart
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] Crash terdeteksi. Restart dalam ${RETRY_DELAY}s..."
  sleep "$RETRY_DELAY"
done

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Max retries ($MAX_RETRIES) tercapai. Watchdog berhenti."
