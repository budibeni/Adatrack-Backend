#!/usr/bin/env bash
# ============================================================================
# e2e-fuel.sh — end-to-end verification of phase B5a (fuel sensor)
# ============================================================================
# Alur nyata yang diuji (PRD Modul 7):
#   frame GT06 0x94/0x0D (`!AIOIL`) → ingestion-tcp → NATS
#   → worker-live (live state fuel) + worker-persistence (td_fuel_logs)
#   → worker-alert (FUEL_DROP: alert.fuel.<company> + notify.alert.<vehicle_id>)
#   → service-websocket (push WS)
#   → REST /vehicles/{id}/fuel/history (FR-7.7)
#
# Prasyarat: infra (PostgreSQL/Redis/NATS) reachable — `make up`.
# Catatan run berulang:
#   * FUEL_TANK_HEIGHT_CM > 0 (default lokal 100) wajib, supaya tinggi sensor
#     dikonversi ke fuel_level/volume (FR-7.3/FR-7.8) dan delta bisa dibandingkan.
#   * ALERT_DEDUP_WINDOW_SEC diperkecil (default 300 → 5) karena engine menyimpan
#     window dedup di MEMORI: tanpa itu run kedua dalam 5 menit tersuppress.
#   * counter rate-limit login/API dibersihkan supaya run berulang tidak kena 429.
#
# Usage: scripts/e2e-fuel.sh [flags]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

FUEL_TANK_HEIGHT_CM="${FUEL_TANK_HEIGHT_CM:-100}"
if [[ "${FUEL_TANK_HEIGHT_CM%%.*}" -le 0 ]]; then
  echo "e2e-fuel: FUEL_TANK_HEIGHT_CM harus > 0 agar fuel_level terkalibrasi" >&2
  exit 1
fi
export FUEL_TANK_HEIGHT_CM
export ALERT_DEDUP_WINDOW_SEC="${ALERT_DEDUP_WINDOW_SEC:-5}"

echo "e2e-fuel: variant=$VARIANT migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

# Dev/E2E saja: buang counter rate-limit login/API agar run berulang tidak 429.
if command -v redis-cli >/dev/null 2>&1; then
  for pat in 'adatrack_gps:auth:login:*' 'adatrack_gps:auth:api:*'; do
    while IFS= read -r key; do
      [[ -n "$key" ]] || continue
      redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" del "$key" >/dev/null 2>&1 || true
    done < <(redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" --scan --pattern "$pat" 2>/dev/null || true)
  done
  echo "e2e-fuel: rate-limit counters cleared"
fi

echo "e2e-fuel: starting services (host mode, FUEL_TANK_HEIGHT_CM=${FUEL_TANK_HEIGHT_CM}, dedup=${ALERT_DEDUP_WINDOW_SEC}s)"
"$ROOT/scripts/start-services.sh" up

wait_health() {
  local name="$1" addr="$2"
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1$addr/healthz" >/dev/null 2>&1; then
      echo "e2e-fuel: ${name} ready (${addr}/healthz)"
      return 0
    fi
    sleep 1
  done
  echo "e2e-fuel: ${name} tidak siap pada ${addr}/healthz" >&2
  return 1
}

wait_health ingestion-tcp "${INGESTION_METRICS_ADDR:-:8090}"
wait_health worker-live "${LIVE_METRICS_ADDR:-:8091}"
wait_health worker-persistence "${PERSISTENCE_METRICS_ADDR:-:8092}"
wait_health worker-alert "${ALERT_METRICS_ADDR:-:8094}"
wait_health service-websocket "${HTTP_ADDR:-:8082}"
wait_health api-vehicle "${API_VEHICLE_HTTP_ADDR:-:8081}"

ARGS=(
  "--ws-base=http://127.0.0.1${HTTP_ADDR:-:8082}"
  "--api-base=http://127.0.0.1${API_VEHICLE_HTTP_ADDR:-:8081}"
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
)

export E2E_NATS_URL="nats://127.0.0.1:${HOST_NATS_PORT:-4222}"
export E2E_REDIS_ADDR="127.0.0.1:${HOST_REDIS_PORT:-6380}"
export REDIS_KEY_PREFIX="${REDIS_KEY_PREFIX:-adatrack_gps:}"

echo "e2e-fuel: running harness"
set +e
(cd "$ROOT/tools/e2e-fuel" && go run . "${ARGS[@]}" "$@")
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "e2e-fuel: FAILED (exit $status) — log service:" >&2
  for f in "$ROOT"/logs/ingestion-tcp.log "$ROOT"/logs/worker-live.log "$ROOT"/logs/worker-persistence.log "$ROOT"/logs/worker-alert.log "$ROOT"/logs/service-websocket.log; do
    [[ -f "$f" ]] || continue
    echo "--- $(basename "$f") ---" >&2
    tail -n 20 "$f" >&2
  done
fi

exit "$status"
