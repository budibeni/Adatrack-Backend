#!/usr/bin/env bash
# ============================================================================
# cleanup-endurance-auto.sh — Tunggu chunked endurance SELESAI, lalu otomatis
# jalankan cleanup-b4-endurance-data.sh (hapus semua data test B4).
#
# Memantau $HOME/b4_chunked_endurance/state.txt hingga STATUS=completed dan
# tidak ada proses loadtest/run-endurance-chunked yang tersisa, kemudian
# memanggil pembersih. Aman dijalankan kapan saja (idempotent).
#
# Pemakaian:
#   ./cleanup-endurance-auto.sh            # foreground
#   ./cleanup-endurance-auto.sh &          # background (atau nohup + disown)
#
# CATATAN: jika WSL/Docker restart, proses watcher ikut mati. Setelah restart,
# jalankan ulang skrip ini (endurance sendiri di-resume endurance-watchdog.sh).
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_FILE="$HOME/b4_chunked_endurance/state.txt"
LOG="$HOME/b4_endurance_cleanup.log"
POLL="${POLL_SECONDS:-60}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" | tee -a "$LOG"
}

endurance_procs() {
  pgrep -f "loadtest/loadtest" >/dev/null 2>&1 || pgrep -f "run-endurance-chunked.sh" >/dev/null 2>&1
}

log "Auto-cleanup B4 watcher dimulai (poll ${POLL}s, log=$LOG)."
while true; do
  if [ -f "$STATE_FILE" ] && grep -q "STATUS=completed" "$STATE_FILE"; then
    if endurance_procs; then
      log "STATUS=completed terdeteksi tapi proses endurance masih ada — menunggu proses selesai..."
      sleep "$POLL"
      continue
    fi
    log "ENDURANCE SELESAI — menjalankan cleanup-b4-endurance-data.sh..."
    "$SCRIPT_DIR/cleanup-b4-endurance-data.sh" 2>&1 | tee -a "$LOG"
    rc=${PIPESTATUS[0]}
    log "Cleanup selesai (exit=$rc). Watcher berhenti."
    exit "$rc"
  fi

  if ! endurance_procs && [ ! -f "$STATE_FILE" ]; then
    log "NOTICE: endurance tidak berjalan & state file tidak ada (belum pernah dimulai / sudah dibersihkan). Menunggu 5 menit..."
    sleep 300
    continue
  fi

  sleep "$POLL"
done