# ============================================================================
# api-vehicle — Dockerfile (build context: backend/)
# ============================================================================
# REST fleet management (PRD §8.2, fase B3): /api/v1/vehicles|geofences|routes|
# speed-configs|alerts + /healthz + /metrics. Auth memakai JWT (HS256) + Redis
# denylist yang SAMA dengan service-websocket, jadi JWT_SECRET dan
# AUTH_*_PREFIX harus identik antar kedua service (lihat x-app-env di
# deployments/docker-compose.coolify.yml).
#
# Sesuai PRD §14.5 step 1: database/ (migrasi + seed) ikut ke dalam image supaya
# artefak migrasi selalu seversi dengan kode yang di-deploy dan
# MIGRATE_ON_BOOT bisa berjalan tanpa akses repo.
#
# Multi-stage: static build (CGO disabled) -> runtime alpine non-root.
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

# Shared module dulu supaya layer cache lebih efektif.
COPY internal/ ./internal/
COPY services/api-vehicle/ ./services/api-vehicle/

WORKDIR /src/services/api-vehicle
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api-vehicle .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Artefak migrasi + helper wait (§14.5).
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/api-vehicle /app/api-vehicle

USER adatrack

# /healthz + /metrics (API_VEHICLE_HTTP_ADDR, default :8081).
EXPOSE 8081

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8081/healthz || exit 1

ENTRYPOINT ["/app/api-vehicle"]
