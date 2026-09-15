#!/usr/bin/env bash
# ============================================================================
# e2e-pipeline.sh — end-to-end verification of the B1 telemetry pipeline
# ============================================================================
# Device frame → ingestion-tcp → NATS → worker-live (Redis) + worker-persistence
# (PostgreSQL). Asserts every hop, then reports PASS/FAIL (exit 1 on failure).
#
# Usage:
#   scripts/e2e-pipeline.sh                     # single-device flow
#   scripts/e2e-pipeline.sh --load --rate=1000 --duration=10s
#   COMPOSE_VARIANT=coolify scripts/e2e-pipeline.sh
#
# Prerequisites: infra reachable (PostgreSQL/Redis/NATS), migrations applied
# (the script runs scripts/migrate.sh itself — idempotent).
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

PGWAIT_ARGS=("$ROOT/scripts/pg-wait.sh" 60)

echo "e2e: variant=$VARIANT migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

echo "e2e: starting pipeline services (host mode)"
"$ROOT/scripts/start-services.sh" up

# Wait for every /healthz to answer before driving traffic.
wait_health() {
  local name="$1" addr="$2"
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1$addr/healthz" >/dev/null 2>&1; then
      echo "e2e: ${name} ready (${addr}/healthz)"
      return 0
    fi
    sleep 1
  done
  echo "e2e: ${name} did not become ready (${addr}/healthz)" >&2
  return 1
}

wait_health ingestion-tcp "${INGESTION_METRICS_ADDR:-:8090}"
wait_health worker-live "${LIVE_METRICS_ADDR:-:8091}"
wait_health worker-persistence "${PERSISTENCE_METRICS_ADDR:-:8092}"

# Host-side connection details for the harness (compose service names are not
# resolvable from the host, so the published bind ports are used).
E2E_ARGS=(
  "--tcp=127.0.0.1:${TCP_PORT:-9003}"
  "--nats=nats://127.0.0.1:${HOST_NATS_PORT:-4222}"
  "--redis=127.0.0.1:${HOST_REDIS_PORT:-6380}"
  "--redis-db=${REDIS_DB:-0}"
  "--redis-prefix=${REDIS_KEY_PREFIX:-adatrack_gps:}"
  "--pg-host=127.0.0.1"
  "--pg-port=${HOST_PG_PORT:-5533}"
  "--pg-user=${POSTGRES_USER:-adatrack}"
  "--pg-password=${POSTGRES_PASSWORD:-}"
  "--pg-db=${POSTGRES_DB:-adatrack_gps_db}"
  "--company=${E2E_COMPANY:-DEV001}"
)

echo "e2e: running harness"
set +e
(cd "$ROOT/tools/e2e" && go run . "${E2E_ARGS[@]}" "$@")
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "e2e: FAILED (exit $status) — service logs:" >&2
  for f in "$ROOT"/logs/*.log; do
    [[ -f "$f" ]] || continue
    echo "--- $(basename "$f") ---" >&2
    tail -n 20 "$f" >&2
  done
fi

exit "$status"