#!/usr/bin/env bash
# ============================================================================
# e2e-fleet.sh — end-to-end verification of phases B7.1/B7.2 (+ B7.3/B7.4)
# ============================================================================
# Alur nyata yang diuji (PRD FR-2.5/FR-2.6, §5.9.3):
#   frame GT06 0x22 (drive plan) → ingestion-tcp → worker-live
#     → akumulator odometer + engine hours (FR-2.5)  → tm_vehicles
#     → state machine trip/stop (FR-2.6)             → th_vehicle_trips + td_vehicle_stops
#   → worker-persistence → th_telemetry_logs
#   → service-websocket: /vehicles/{id}/playback (RDP + alamat) + /geocode/reverse
#
# Catatan: start-services.sh memuat ulang .env.<variant> (load_variant_env), jadi
# nilai FLEET_*/TRIP_* di .env.local menang atas export di sini. Harness karena itu
# menunggu sampai flush akumulator benar-benar terjadi (bukan mengandalkan interval
# kecil): default produksi 30 s tetap lolos, hanya lebih lambat.
#
# Harness memulihkan `odometer_km`/`engine_hours` dan menghapus trip yang
# dibuatnya (flag --cleanup) supaya fixture dev tetap bersih.
#   * counter rate-limit login dibersihkan agar run berulang tidak kena 429.
#
# Prasyarat: infra (PostgreSQL/Redis/NATS) reachable — `make up`.
# Usage: scripts/e2e-fleet.sh [flags]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

WAIT_SEC="${E2E_FLEET_WAIT_SEC:-90}"

echo "e2e-fleet: variant=$VARIANT migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

# Dev/E2E saja: buang counter rate-limit login/API agar run berulang tidak 429.
if command -v redis-cli >/dev/null 2>&1; then
  for pat in 'adatrack_gps:auth:login:*' 'adatrack_gps:auth:api:*'; do
    while IFS= read -r key; do
      [[ -n "$key" ]] || continue
      redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" del "$key" >/dev/null 2>&1 || true
    done < <(redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" --scan --pattern "$pat" 2>/dev/null || true)
  done
  echo "e2e-fleet: rate-limit counters cleared"
fi

echo "e2e-fleet: starting services (host mode)"
"$ROOT/scripts/start-services.sh" up

wait_health() {
  local name="$1" addr="$2"
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1$addr/healthz" >/dev/null 2>&1; then
      echo "e2e-fleet: ${name} ready (${addr}/healthz)"
      return 0
    fi
    sleep 1
  done
  echo "e2e-fleet: ${name} tidak siap pada ${addr}/healthz" >&2
  return 1
}

wait_health ingestion-tcp "${INGESTION_METRICS_ADDR:-:8090}"
wait_health worker-live "${LIVE_METRICS_ADDR:-:8091}"
wait_health worker-persistence "${PERSISTENCE_METRICS_ADDR:-:8092}"
wait_health service-websocket "${HTTP_ADDR:-:8082}"

ARGS=(
  "--ws-base=http://127.0.0.1${HTTP_ADDR:-:8082}"
  "--tcp=127.0.0.1:${TCP_PORT:-9003}"
  "--pg-host=127.0.0.1"
  "--pg-port=${HOST_PG_PORT:-5533}"
  "--pg-user=${POSTGRES_USER:-adatrack_gps_user}"
  "--pg-password=${POSTGRES_PASSWORD:-}"
  "--pg-db=${POSTGRES_DB:-adatrack_gps_db}"
  "--company=${E2E_COMPANY:-DEV001}"
  "--imei=${E2E_IMEI:-864201040512345}"
  "--admin-email=${E2E_ADMIN_EMAIL:-admin@dev001.io}"
  "--admin-password=${E2E_ADMIN_PASSWORD:-Admin@123}"
  "--wait=${WAIT_SEC}s"
)

echo "e2e-fleet: running harness"
set +e
(cd "$ROOT/tools/e2e-fleet" && go run . "${ARGS[@]}" "$@")
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "e2e-fleet: FAILED (exit $status) — log service:" >&2
  for f in "$ROOT"/logs/ingestion-tcp.log "$ROOT"/logs/worker-live.log \
           "$ROOT"/logs/worker-persistence.log "$ROOT"/logs/service-websocket.log; do
    [[ -f "$f" ]] || continue
    echo "--- $(basename "$f") ---" >&2
    tail -n 20 "$f" >&2
  done
fi

exit "$status"
