#!/usr/bin/env bash
# Crash-safe service starter for B4 endurance/replication work.
# Usage: ./start-services.sh [tcp_port] [ws_http_port] [ws_metrics_port]
set -uo pipefail

BACKEND_DIR="/home/user/projects/ajb_gps/backend"
ENV_FILE="$BACKEND_DIR/.env"
LOG_DIR="$HOME/b4_endurance/logs"
mkdir -p "$LOG_DIR"

# B4 audit 2026-08-31: refresh file_sd targets Prometheus (IP WSL berubah tiap
# restart); best-effort — kegagalan tidak menghentikan start service.
bash "$(dirname "$0")/gen-prom-targets.sh" >/dev/null 2>&1 || true

TCP_PORT="${1:-9003}"
WS_HTTP="${2:-8082}"
WS_METRICS="${3:-9091}"

export ENV_FILE

echo "=== Starting ingestion-tcp on TCP $TCP_PORT ==="
nohup env TCP_PORT="$TCP_PORT" "$BACKEND_DIR/services/ingestion-tcp/ingestion-tcp" \
  > "$LOG_DIR/ingestion-tcp.log" 2>&1 &
echo "PID=$!"

echo "=== Starting worker-live ==="
nohup "$BACKEND_DIR/services/worker-live/worker-live" \
  > "$LOG_DIR/worker-live.log" 2>&1 &
echo "PID=$!"

echo "=== Starting worker-persistence ==="
nohup "$BACKEND_DIR/services/worker-persistence/worker-persistence" \
  > "$LOG_DIR/worker-persistence.log" 2>&1 &
echo "PID=$!"

echo "=== Starting worker-alert ==="
nohup "$BACKEND_DIR/services/worker-alert/worker-alert" \
  > "$LOG_DIR/worker-alert.log" 2>&1 &
echo "PID=$!"

echo "=== Starting service-websocket (HTTP=$WS_HTTP, metrics=$WS_METRICS) ==="
nohup env WEBSOCKET_HTTP_ADDR=":$WS_HTTP" WEBSOCKET_METRICS_ADDR=":$WS_METRICS" \
  "$BACKEND_DIR/services/service-websocket/service-websocket" \
  > "$LOG_DIR/service-websocket.log" 2>&1 &
echo "PID=$!"

echo "=== Starting api-vehicle ==="
nohup "$BACKEND_DIR/services/api-vehicle/api-vehicle" \
  > "$LOG_DIR/api-vehicle.log" 2>&1 &
echo "PID=$!"

echo "=== Starting service-media ==="
nohup "$BACKEND_DIR/services/service-media/service-media" \
  > "$LOG_DIR/service-media.log" 2>&1 &
echo "PID=$!"

echo "=== All services started. Waiting for health... ==="
sleep 5
echo "=== HEALTH CHECKS ==="
for entry in "8090:ingestion" "8091:persistence" "8092:live" "8093:alert" "9091:ws-metrics" "8082:ws-http" "8081:api-vehicle" "8095:media"; do
  p="${entry#*:}"; port="${entry%%:*}"
  echo -n "$p(:$port): "
  curl -s --max-time 2 "http://127.0.0.1:${port}/healthz" 2>/dev/null || echo "FAIL"
  echo
done
