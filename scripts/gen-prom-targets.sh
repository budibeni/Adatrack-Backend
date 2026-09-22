#!/usr/bin/env bash
# ============================================================================
# gen-prom-targets.sh — regenerate Prometheus file_sd targets from the live
# service health ports (B4, PRD §10.4 / §14.6 step 5).
# Mirrors scripts/start-services.sh so the monitoring stack always scrapes the
# exact /metrics listeners the services expose.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh" 2>/dev/null || true
if [[ -f "$ROOT/.env.${COMPOSE_VARIANT:-local}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ROOT/.env.${COMPOSE_VARIANT:-local}"
  set +a
fi

TARGETS_FILE="${1:-$ROOT/monitoring/targets/adatrack-services.json}"
# detect_host_ip prints the host's primary IPv4 (127.0.0.1 as last resort).
detect_host_ip() {
  local ip
  ip="$(ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')"
  if [[ -z "$ip" ]]; then
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
  fi
  if [[ -z "$ip" ]]; then
    ip="127.0.0.1"
  fi
  echo "$ip"
}

# PROM_SCRAPE_HOST selects the host address a (possibly containerised)
# Prometheus scrapes. When unset it is auto-detected: the host's primary
# non-loopback IPv4 is reachable from a bridge container on a normal Linux
# Docker host, while `host.docker.internal` (extra_hosts:host-gateway, set on
# the `prometheus` service in backend/docker-compose.yml) covers Docker Desktop.
TARGET_HOST="${PROM_SCRAPE_HOST:-$(detect_host_ip)}"
mkdir -p "$(dirname "$TARGETS_FILE")"

addr() { local v="$1" d="$2"; v="${v:-$d}"; v="${v#:}"; echo "$TARGET_HOST:$v"; }

{
  printf '[\n'
  printf '  {"targets": ["%s"], "labels": {"service": "ingestion-tcp", "env": "local"}}' "$(addr "${INGESTION_METRICS_ADDR:-}" 8090)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-live", "env": "local"}}' "$(addr "${LIVE_METRICS_ADDR:-}" 8091)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-persistence", "env": "local"}}' "$(addr "${PERSISTENCE_METRICS_ADDR:-}" 8092)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "worker-alert", "env": "local"}}' "$(addr "${ALERT_METRICS_ADDR:-}" 8094)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "service-websocket", "env": "local"}}' "$(addr "${HTTP_ADDR:-}" 8082)"
  printf ',\n  {"targets": ["%s"], "labels": {"service": "api-vehicle", "env": "local"}}' "$(addr "${API_VEHICLE_HTTP_ADDR:-}" 8081)"
  printf '\n]\n'
} > "$TARGETS_FILE"

echo "gen-prom-targets: wrote $TARGETS_FILE"
cat "$TARGETS_FILE"
