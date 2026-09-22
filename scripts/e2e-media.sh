#!/usr/bin/env bash
# ============================================================================
# e2e-media.sh — end-to-end verification of phase B5b (service-media)
# ============================================================================
# Alur nyata yang diuji (PRD Modul 8 / Scope A):
#   HMAC ingest (multipart | JSON+presigned PUT) → MinIO → th_media_events →
#   presigned GET (byte-persis) → WS MEDIA_EVENT → audit MEDIA_URL_ACCESS →
#   retensi (sweep menghapus objek + status expired)
#
# Script ini menaikkan service host-mode dengan MEDIA_RETENTION_SWEEP_SEC kecil
# supaya sweep retensi bisa diverifikasi dalam hitungan detik (default produksi
# tetap cron 03:00 dari MEDIA_CLEANUP_CRON).
#
# Usage:
#   scripts/e2e-media.sh                       # jalankan seluruh chain
#   scripts/e2e-media.sh --imei=8642... --timeout=20s
#
# Prasyarat: infra (PostgreSQL/Redis/NATS/MinIO) reachable — lihat `make up`.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

# Sweep retensi rapat untuk E2E (menimpa cron 03:00 hanya pada run ini).
export MEDIA_RETENTION_SWEEP_SEC="${MEDIA_RETENTION_SWEEP_SEC:-2}"

# Cache konfigurasi per-company dipersingkat supaya uji negatif "oversize"
# (menurunkan max_file_mb di DB) langsung berlaku tanpa menunggu 60 s.
export MEDIA_CONFIG_CACHE_SEC="${MEDIA_CONFIG_CACHE_SEC:-1}"

# Host-mode overrides: service-media harus bicara ke MinIO yang dipublish host.
export MEDIA_BACKEND="${MEDIA_BACKEND:-s3}"
export MEDIA_S3_ENDPOINT="${MEDIA_S3_ENDPOINT:-http://127.0.0.1:${HOST_MINIO_PORT:-9000}}"

# JWT_SECRET wajib sama dengan service-websocket (dibaca dari .env variant).
if [[ -z "${JWT_SECRET:-}" ]]; then
  echo "e2e-media: JWT_SECRET tidak terbaca dari .env.$VARIANT" >&2
  exit 1
fi

echo "e2e-media: variant=$VARIANT migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

# Dev/E2E saja: buang counter rate-limit login/API agar run berulang tidak 429.
if command -v redis-cli >/dev/null 2>&1; then
  for pat in 'adatrack_gps:auth:login:*' 'adatrack_gps:auth:api:*'; do
    while IFS= read -r key; do
      [[ -n "$key" ]] || continue
      redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" del "$key" >/dev/null 2>&1 || true
    done < <(redis-cli -h 127.0.0.1 -p "${HOST_REDIS_PORT:-6380}" --scan --pattern "$pat" 2>/dev/null || true)
  done
  echo "e2e-media: rate-limit counters cleared"
fi

echo "e2e-media: starting services (host mode, retention sweep ${MEDIA_RETENTION_SWEEP_SEC}s)"
"$ROOT/scripts/start-services.sh" up

wait_health() {
  local name="$1" addr="$2"
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1$addr/healthz" >/dev/null 2>&1; then
      echo "e2e-media: ${name} ready (${addr}/healthz)"
      return 0
    fi
    sleep 1
  done
  echo "e2e-media: ${name} tidak siap pada ${addr}/healthz" >&2
  return 1
}

wait_health service-media "${MEDIA_HTTP_ADDR:-:8095}"
wait_health service-websocket "${HTTP_ADDR:-:8082}"

ARGS=(
  "--media-base=http://127.0.0.1${MEDIA_HTTP_ADDR:-:8095}"
  "--ws-base=http://127.0.0.1${HTTP_ADDR:-:8082}"
  "--api-base=http://127.0.0.1${API_VEHICLE_HTTP_ADDR:-:8081}"
  "--pg-host=127.0.0.1"
  "--pg-port=${HOST_PG_PORT:-5533}"
  "--pg-user=${POSTGRES_USER:-adatrack_gps_user}"
  "--pg-password=${POSTGRES_PASSWORD:-}"
  "--pg-db=${POSTGRES_DB:-adatrack_gps_db}"
  "--company=${E2E_COMPANY:-DEV001}"
  "--imei=${E2E_IMEI:-864201040512345}"
  "--admin-email=${E2E_ADMIN_EMAIL:-admin@dev001.io}"
  "--admin-password=${E2E_ADMIN_PASSWORD:-Admin@123}"
  "--hmac-secret=${MEDIA_HMAC_SECRET:-}"
)

echo "e2e-media: running harness"
set +e
(cd "$ROOT/tools/e2e-media" && go run . "${ARGS[@]}" "$@")
status=$?
set -e

if [[ "$status" -ne 0 ]]; then
  echo "e2e-media: FAILED (exit $status) — log service:" >&2
  for f in "$ROOT"/logs/service-media.log "$ROOT"/logs/service-websocket.log; do
    [[ -f "$f" ]] || continue
    echo "--- $(basename "$f") ---" >&2
    tail -n 25 "$f" >&2
  done
fi

exit "$status"
