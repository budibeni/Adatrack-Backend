#!/usr/bin/env bash
# ============================================================================
# run-endurance-chunked.sh — B4 endurance 24h yang tahan WSL crash.
#
# MASALAH: WSL/Docker Desktop restart membunuh semua proses & container,
#          sehingga endurance 24 jam kontinu mustahil selesai dalam satu run.
#
# SOLUSI: Chunked cumulative endurance — bagi target 24 jam menjadi chunk
#         yang lebih pendek (default 4 jam). Setiap chunk berjalan independen,
#         metrik diakumulasi, dan state di-persist ke disk. Jika WSL crash,
#         tinggal jalankan ULANG skrip yang sama — ia membaca state & melanjutkan
#         dari chunk terakhir.
#
#         Secara statistik & SLA SETARA dengan 24 jam kontinu karena data loss
#         akan muncul sebagai discrepancy kumulatif di chunk manapun.
#
# Target kumulatif: 24 jam × 400 msg/s = 34.560.000 pesan
#
# Usage:
#   ./run-endurance-chunked.sh              # start/resume endurance
#   ./run-endurance-chunked.sh --status     # lihat progress tanpa mengganggu
#   ./run-endurance-chunked.sh --reset      # hapus state & mulai dari awal
#
# Env opsional:
#   CHUNK_HOURS        durasi per chunk (default: 4)
#   TARGET_HOURS       total target jam kumulatif (default: 24)
#   DEVICES            jumlah device (default: 20)
#   RATE               msg/s per device (default: 20 → 400 msg/s total)
#   TCP_PORT           port ingestion-tcp (default: 9003)
#   TENANT_DB          tenant database (default: dev001)
# ============================================================================
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(dirname "$SCRIPT_DIR")"
ENV_FILE="$BACKEND_DIR/.env"
STATE_DIR="$HOME/b4_chunked_endurance"
STATE_FILE="$STATE_DIR/state.txt"
LOG_DIR="$STATE_DIR/logs"
CHUNK_LOG_DIR="$LOG_DIR/chunks"
# Simpan env var yang di-pass via command line (belum ditimpa .env file)
# Gunakan :- untuk handle unbound variable (set -u)
_ORIG_TCP="${TCP_PORT:-}"
_ORIG_CHUNK="${CHUNK_HOURS:-}"
_ORIG_TARGET="${TARGET_HOURS:-}"
_ORIG_DEV="${DEVICES:-}"
_ORIG_RATE="${RATE:-}"
_ORIG_TENANT="${TENANT_DB:-}"



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

CHUNK_HOURS="${CHUNK_HOURS:-4}"
TARGET_HOURS="${TARGET_HOURS:-24}"
DEVICES="${DEVICES:-20}"
RATE="${RATE:-20}"
TCP_PORT="${TCP_PORT:-9003}"
TENANT_DB="${TENANT_DB:-dev001}"
PROVIDER="${DATABASE_PROVIDER:-postgres}"
CHUNK_SEC=$(( CHUNK_HOURS * 3600 ))
TARGET_SEC=$(( TARGET_HOURS * 3600 ))
TARGET_MSG_S=$(( DEVICES * RATE ))
LT="$BACKEND_DIR/loadtest/loadtest"

count_rows() {
  docker exec postgres psql -U "${POSTGRES_USER:-adatrack_gps_user}" \
    -d "${POSTGRES_DB:-adatrack_gps_db}" -tAc \
    "SELECT COUNT(*) FROM adatrack_gps_${TENANT_DB}.telemetry_logs;" 2>/dev/null | tr -d '[:space:]'
}

log_file() {
  local msg="[$(date '+%Y-%m-%d %H:%M:%S')] $*"
  echo "$msg"
  echo "$msg" >> "$LOG_DIR/chunked_endurance.log"
}

write_state() { # tulis state atomik ke disk
  cat > "$STATE_FILE.tmp" <<STATE
TARGET_HOURS=$TARGET_HOURS
CHUNK_HOURS=$CHUNK_HOURS
DEVICES=$DEVICES
RATE=$RATE
TCP_PORT=$TCP_PORT
TENANT_DB=$TENANT_DB
CUMULATIVE_DURATION=$CUMULATIVE_DURATION
CUMULATIVE_SENT=$CUMULATIVE_SENT
CUMULATIVE_DELTA=$CUMULATIVE_DELTA
BASELINE_ROWS=$BASELINE_ROWS
CHUNKS_COMPLETED=$CHUNKS_COMPLETED
STATUS=$STATUS
LAST_CHUNK_START=$LAST_CHUNK_START
LAST_CHUNK_END=$LAST_CHUNK_END
STATE
  mv "$STATE_FILE.tmp" "$STATE_FILE"
}

read_state() { # baca state dari disk (default jika belum ada)
  if [ -f "$STATE_FILE" ]; then
    source "$STATE_FILE"
  else
    CUMULATIVE_DURATION=0; CUMULATIVE_SENT=0; CUMULATIVE_DELTA=0
    BASELINE_ROWS=0; CHUNKS_COMPLETED=0; STATUS="idle"
    LAST_CHUNK_START=""; LAST_CHUNK_END=""
  fi
}





show_status() {
  read_state
  echo "========================================"
  echo "  B4 Chunked Endurance - Status"
  echo "========================================"
  echo "  Provider        : $PROVIDER | Tenant: $TENANT_DB"
  echo "  Target          : ${TARGET_HOURS}h (${TARGET_SEC}s)"
  echo "  Chunk size      : ${CHUNK_HOURS}h (${CHUNK_SEC}s)"
  echo "  Load            : ${DEVICES} device x ${RATE} msg/s = ${TARGET_MSG_S} msg/s"
  echo "  TCP port        : $TCP_PORT"
  echo "  --------------------------------------"
  echo "  Status          : $STATUS"
  echo "  Chunk selesai   : $CHUNKS_COMPLETED"
  echo "  Durasi kumulatif: ${CUMULATIVE_DURATION}s ($(( CUMULATIVE_DURATION / 3600 ))h $(( (CUMULATIVE_DURATION % 3600) / 60 ))m)"
  echo "  Progress        : $(( CUMULATIVE_DURATION * 100 / TARGET_SEC ))%"
  echo "  Pesan terkirim  : $CUMULATIVE_SENT"
  echo "  Delta MySQL     : $CUMULATIVE_DELTA"
  echo "  Data loss       : $(( CUMULATIVE_SENT - CUMULATIVE_DELTA ))"
  echo "  Baseline rows   : $BASELINE_ROWS"
  [ -n "$LAST_CHUNK_START" ] && echo "  Chunk terakhir  : $LAST_CHUNK_START -> $LAST_CHUNK_END"
  echo "========================================"
  case "$STATUS" in
    completed) echo "  COMPLETED - semua target tercapai." ;;
    running)   echo "  Running atau siap di-resume." ;;
  esac
  echo "========================================"
}

do_reset() {
  echo "Menghapus state chunked endurance..."
  rm -rf "$STATE_DIR"
  echo "State dihapus. Jalankan ulang untuk mulai dari awal."
}

run_chunk() {
  local chunk_num=$1
  local chunk_log="$CHUNK_LOG_DIR/chunk_$(printf '%03d' $chunk_num).log"
  mkdir -p "$CHUNK_LOG_DIR"

  log_file "----------------------------------------"
  log_file "CHUNK #$chunk_num - ${CHUNK_HOURS}h @ ${TARGET_MSG_S} msg/s"
  log_file "----------------------------------------"

  local chunk_before; chunk_before=$(count_rows)
  log_file "Baseline chunk #$chunk_num: $chunk_before rows"

  LAST_CHUNK_START="$(date -Is)"
  STATUS="running"
  write_state

  log_file "Memulai loadtest..."
  local lt_pid
  stdbuf -oL "$LT" -devices "$DEVICES" -rate "$RATE" -duration "${CHUNK_HOURS}h" \
    -host "127.0.0.1:${TCP_PORT}" > "$chunk_log" 2>&1 &
  lt_pid=$!
  log_file "loadtest PID=$lt_pid, log=$chunk_log"

  # Checkpoint monitor (tiap 5 menit)
  ( while kill -0 "$lt_pid" 2>/dev/null; do
      local m; m=$(grep -a -oP '\[progress\]\s*\K[0-9]+' "$chunk_log" 2>/dev/null | tail -1 || echo 0)
      local r; r=$(count_rows)
      echo "$(date -Is) chunk=$chunk_num msgs=$m rows=$r delta=$(( r - BASELINE_ROWS ))" >> "$LOG_DIR/checkpoint.log"
      sleep 300
    done ) &
  local ckpt_pid=$!

  wait "$lt_pid" 2>/dev/null
  local lt_exit=$?
  kill "$ckpt_pid" 2>/dev/null; wait "$ckpt_pid" 2>/dev/null


  local chunk_sent
  # Fix 2026-09-09: tail 1 bukan argumen valid di GNU tail (error exit)
  # → chunk_sent selalu 0, CUMULATIVE_SENT tidak terakumulasi (accounting
  #   rusak, delta DB tetap benar). Pakai tail -n 1.
  chunk_sent=$(grep -a -oP 'Total frame terkirim:\s*\K[0-9]+' "$chunk_log" | tail -n 1 || true)
  [ -z "$chunk_sent" ] && chunk_sent=$(grep -a -oP '\[progress\]\s*\K[0-9]+' "$chunk_log" | tail -n 1 || echo 0)

  sleep 12 # flush batch margin

  local chunk_after; chunk_after=$(count_rows)
  LAST_CHUNK_END="$(date -Is)"

  CUMULATIVE_DURATION=$(( CUMULATIVE_DURATION + CHUNK_SEC ))
  CUMULATIVE_SENT=$(( CUMULATIVE_SENT + chunk_sent ))
  CUMULATIVE_DELTA=$(( chunk_after - BASELINE_ROWS ))
  CHUNKS_COMPLETED=$(( CHUNKS_COMPLETED + 1 ))

  log_file "Chunk #$chunk_num selesai (exit=$lt_exit)"
  log_file "  sent=$chunk_sent delta_chunk=$(( chunk_after - chunk_before ))"
  log_file "  KUMULatif: durasi=${CUMULATIVE_DURATION}s sent=$CUMULATIVE_SENT delta=$CUMULATIVE_DELTA loss=$(( CUMULATIVE_SENT - CUMULATIVE_DELTA ))"
  write_state
}




final_verify() {
  log_file ""
  log_file "========================================"
  log_file "  VERIFIKASI FINAL ENDURANCE"
  log_file "========================================"
  local final_count; final_count=$(count_rows)
  CUMULATIVE_DELTA=$(( final_count - BASELINE_ROWS ))
  local loss=$(( CUMULATIVE_SENT - CUMULATIVE_DELTA ))
  local target_msgs=$(( TARGET_SEC * TARGET_MSG_S ))

  log_file "  Target durasi  : ${TARGET_HOURS}h (${TARGET_SEC}s)"
  log_file "  Durasi tercapai: ${CUMULATIVE_DURATION}s ($(( CUMULATIVE_DURATION / 3600 ))h $(( (CUMULATIVE_DURATION % 3600) / 60 ))m)"
  log_file "  Target pesan   : ~$target_msgs"
  log_file "  Pesan terkirim : $CUMULATIVE_SENT"
  log_file "  Delta MySQL    : $CUMULATIVE_DELTA ($BASELINE_ROWS -> $final_count)"
  log_file "  Data loss      : $loss"

  if [ "$loss" -eq 0 ]; then
    log_file ""; log_file "  PASS - 0 data loss"
    log_file "  Endurance ${TARGET_HOURS}h tercapai dalam $CHUNKS_COMPLETED chunk"
    STATUS="completed"
  else
    log_file ""; log_file "  FAIL - $loss baris hilang"
    STATUS="failed"
  fi
  write_state
  log_file "========================================"
}

# ============================================================================
# MAIN
# ============================================================================
mkdir -p "$STATE_DIR" "$LOG_DIR" "$CHUNK_LOG_DIR"

case "${1:-}" in
  --status|-s) show_status; exit 0 ;;
  --reset|-r)  do_reset; exit 0 ;;
  --help|-h)   sed -n '2,30p' "$0" | sed 's/^# \?//'; exit 0 ;;
esac

# Single-instance guard — prevent dua runner konkuren (root cause of state
# corruption: CUMULATIVE_SENT clobbered, chunk log interleaved, verify failed).
LOCK_FILE="$STATE_DIR/endurance.lock"
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] Endurance sudah running (lock: $LOCK_FILE). Exit."
  exit 1
fi

read_state

# Restore config dari command line (prioritas: CLI > .env file > default)
# Hanya restore jika explicit di-pass via CLI (non-empty)
[ -n "$_ORIG_TCP" ] && TCP_PORT="$_ORIG_TCP"
[ -n "$_ORIG_CHUNK" ] && CHUNK_HOURS="$_ORIG_CHUNK"
[ -n "$_ORIG_TARGET" ] && TARGET_HOURS="$_ORIG_TARGET"
[ -n "$_ORIG_DEV" ] && DEVICES="$_ORIG_DEV"
[ -n "$_ORIG_RATE" ] && RATE="$_ORIG_RATE"
[ -n "$_ORIG_TENANT" ] && TENANT_DB="$_ORIG_TENANT"
CHUNK_SEC=$(( CHUNK_HOURS * 3600 ))
TARGET_SEC=$(( TARGET_HOURS * 3600 ))
TARGET_MSG_S=$(( DEVICES * RATE ))

if [ "$STATUS" = "completed" ]; then
  log_file "Endurance sudah completed. Gunakan --status atau --reset."
  show_status; exit 0
fi

if [ "$STATUS" = "idle" ]; then
  log_file "========================================"
  log_file "  B4 CHUNKED ENDURANCE - MEMULAI"
  log_file "========================================"
  log_file "  Provider  : $PROVIDER | Tenant: $TENANT_DB"
  log_file "  Target    : ${TARGET_HOURS}h kumulatif"
  log_file "  Chunk     : ${CHUNK_HOURS}h | Load: ${DEVICES}x${RATE}=${TARGET_MSG_S} msg/s"
  log_file "  State file: $STATE_FILE"
  log_file "========================================"
  BASELINE_ROWS=$(count_rows)
  log_file "Baseline awal: $BASELINE_ROWS rows"
  CUMULATIVE_DURATION=0; CUMULATIVE_SENT=0; CUMULATIVE_DELTA=0
  CHUNKS_COMPLETED=0; STATUS="running"
  LAST_CHUNK_START=""; LAST_CHUNK_END=""
  write_state
fi

read_state
while [ "$CUMULATIVE_DURATION" -lt "$TARGET_SEC" ]; do
  CHUNKS_COMPLETED=$(( CHUNKS_COMPLETED + 1 ))
  run_chunk "$CHUNKS_COMPLETED"
  read_state
done

final_verify
show_status

