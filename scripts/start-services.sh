#!/usr/bin/env bash
# ============================================================================
# start-services.sh — run the B1 pipeline services on the dev host (PRD §14.3)
# ============================================================================
# Infra (PostgreSQL/Redis/NATS) is expected to run in compose; this helper runs
# the Go services in the FOREGROUND host context, overriding hosts/ports to the
# published bind ports (HOST_* in .env.local) and writing Prometheus file_sd
# targets (PRD §14.6 step 5).
#
# Usage:
#   scripts/start-services.sh up      # build + start (background), logs in logs/
#   scripts/start-services.sh down    # stop everything started by `up`
#   scripts/start-services.sh logs    # tail all logs
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

load_variant_env "${COMPOSE_VARIANT:-local}"

LOG_DIR="$ROOT/logs"
PID_DIR="$LOG_DIR/pids"
TARGETS_FILE="$ROOT/monitoring/targets/adatrack-services.json"
SERVICES=(ingestion-tcp worker-live worker-persistence worker-alert service-websocket api-vehicle service-media)

# Host-side overrides: published infra ports + loopback hosts.
export POSTGRES_HOST=127.0.0.1
export POSTGRES_PORT="${HOST_PG_PORT:-5533}"
export REDIS_HOST=127.0.0.1
export REDIS_PORT="${HOST_REDIS_PORT:-6380}"
export NATS_URL="nats://127.0.0.1:${HOST_NATS_PORT:-4222}"

build_and_start() {
  mkdir -p "$PID_DIR" "$(dirname "$TARGETS_FILE")"

  # Stop leftovers from a previous run first: a stale process would hold the
  # listeners and the "new" service would silently fail to bind (idempotent up).
  stop_all

  for svc in "${SERVICES[@]}"; do
    echo "start-services: building ${svc}"
    mkdir -p "$ROOT/bin"
    (cd "$ROOT/services/$svc" && go build -o "$ROOT/bin/$svc" .)
  done

  for svc in "${SERVICES[@]}"; do
    echo "start-services: starting ${svc}"
    (cd "$ROOT" && nohup "$ROOT/bin/$svc" > "$LOG_DIR/$svc.log" 2>&1 & echo $! > "$PID_DIR/$svc.pid")
  done

  sleep 2
  # Target Prometheus di-generate oleh SATU sumber kebenaran: gen-prom-targets.sh.
  # Sebelumnya blok ini menulis alamat `127.0.0.1` sendiri, dan itu SELALU salah di
  # sini: dari sudut container Prometheus, 127.0.0.1 adalah loopback container itu
  # sendiri, sehingga seluruh target aplikasi DOWN (dan menimpa hasil generator yang
  # sudah benar setiap kali `make services-up` dijalankan).
  # Non-fatal: target Prometheus itu pelengkap, kegagalan menulisnya tidak boleh
  # menggagalkan start service (script ini memakai `set -e`).
  "$ROOT/scripts/gen-prom-targets.sh" >/dev/null \
    || echo "start-services: PERINGATAN: gagal menulis target Prometheus" >&2
  echo "start-services: targets ditulis oleh scripts/gen-prom-targets.sh"
  for svc in "${SERVICES[@]}"; do
    local pid
    pid="$(cat "$PID_DIR/$svc.pid")"
    if kill -0 "$pid" 2>/dev/null; then
      echo "start-services: ${svc} running (pid ${pid}, log logs/${svc}.log)"
    else
      echo "start-services: ${svc} FAILED to start — see logs/${svc}.log" >&2
    fi
  done
}

stop_all() {
  for svc in "${SERVICES[@]}"; do
    if [[ -f "$PID_DIR/$svc.pid" ]]; then
      local pid
      pid="$(cat "$PID_DIR/$svc.pid")"
      if kill -0 "$pid" 2>/dev/null; then
        echo "start-services: stopping ${svc} (pid ${pid})"
        kill "$pid" 2>/dev/null || true
      fi
      rm -f "$PID_DIR/$svc.pid"
    fi
    # Catch processes started outside this script (e.g. a previous manual run)
    # so a stale listener can never shadow the one being started.
    pkill -f "$ROOT/bin/$svc" 2>/dev/null || true
  done
  sleep 1
}

case "${1:-up}" in
  up)   build_and_start ;;
  down) stop_all ;;
  logs) tail -n 50 -f "$LOG_DIR"/*.log ;;
  *)    echo "usage: start-services.sh [up|down|logs]" >&2; exit 1 ;;
esac