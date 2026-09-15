#!/usr/bin/env bash
# ============================================================================
# e2e-websocket.sh — end-to-end verification of phase B2 (service-websocket)
# ============================================================================
# Verifies, against the REAL services (no mocks):
#   login → JWT/refresh/logout → RBAC row-level → REST contract →
#   device frame → ingestion-tcp → worker-live → service-websocket → WS client
#   → audit trail rows in master.tm_audit_logs
#
# Usage:
#   scripts/e2e-websocket.sh                     # full flow
#   scripts/e2e-websocket.sh --timeout=20s       # slower environments
#   COMPOSE_VARIANT=coolify scripts/e2e-websocket.sh
#
# Prerequisites: infra reachable (PostgreSQL/Redis/NATS); the script applies
# migrations itself (idempotent) and starts the host services.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

echo "e2e-ws: variant=$VARIANT migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

echo "e2e-ws: starting services (host mode)"
"$ROOT/scripts/start-services.sh" up

wait_health() {
  local name="$1" addr="$2"
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1$addr/healthz" >/dev/null 2>&1; then
      echo "e2e-ws: ${name} ready (${addr}/healthz)"
      return 0
    fi
    sleep 1
  done
  echo "e2e-ws: ${name} did not become ready (${addr}/healthz)" >&2
  return 1
}

wait_health ingestion-tcp "${INGESTION_METRICS_ADDR:-:8090}"
wait_health worker-live "${LIVE_METRICS_ADDR:-:8091}"
wait_health service-websocket "${HTTP_ADDR:-:8082}"

# Host-side connection details (compose service names are not resolvable here).
E2E_ARGS=(
  "--base=http://127.0.0.1${HTTP_ADDR:-:8082}"
  "--tcp=127.0.0.1:${TCP_PORT:-9003}"
  "--imei=${E2E_IMEI:-864201040512345}"
  "--plate=${E2E_PLATE:-B 1234 XYZ}"
  "--company=${E2E_COMPANY:-DEV001}"
  "--admin-email=${E2E_ADMIN_EMAIL:-admin@dev001.io}"
  "--admin-password=${E2E_ADMIN_PASSWORD:-Admin@123}"
  "--platform-email=${E2E_PLATFORM_EMAIL:-platform@adatrackgps.local}"
  "--platform-password=${E2E_PLATFORM_PASSWORD:-Platform@123}"
  "--driver-email=${E2E_DRIVER_EMAIL:-driver@dev001.io}"
  "--driver-password=${E2E_DRIVER_PASSWORD:-Admin@123}"
  "--pg-host=${POSTGRES_HOST:-127.0.0.1}"
  "--pg-port=${POSTGRES_PORT:-${HOST_PG_PORT:-5533}}"
  "--pg-user=${POSTGRES_USER:-adatrack}"
  "--pg-password=${POSTGRES_PASSWORD:-}"
  "--pg-db=${POSTGRES_DB:-adatrack_gps_db}"
  "--master-schema=${MASTER_DB_NAME:-adatrack_gps_master}"
)

echo "e2e-ws: running harness"
set +e
(cd "$ROOT/tools/e2ews" && go run . "${E2E_ARGS[@]}" "$@")
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "e2e-ws: FAILED (exit $status) — service logs:" >&2
  for f in "$ROOT"/logs/*.log; do
    [[ -f "$f" ]] || continue
    echo "--- $(basename "$f") ---" >&2
    tail -n 20 "$f" >&2
  done
fi

exit "$status"