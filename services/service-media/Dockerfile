# ============================================================================
# service-media — Dockerfile (build context: backend/)
# ============================================================================
# Dashcam event media (PRD Modul 8 / Scope A, fase B5b): ingest HMAC, katalog
# `th_media_events`, presigned GET dan sweep retensi. REST + /healthz + /metrics
# pada MEDIA_HTTP_ADDR (8095) dan MEDIA_METRICS_ADDR (8096, §14.2).
#
# PRD §14.5 step 1: database/ (migrasi + seed) WAJIB ikut ke dalam image supaya
# MIGRATE_ON_BOOT bisa meng-apply migrasi master dari repo (bukan volume manual).
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY internal/ ./internal/
COPY services/service-media/ ./services/service-media/

WORKDIR /src/services/service-media
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/service-media .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Artefak migrasi + helper wait (§14.5 step 1) untuk MIGRATE_ON_BOOT.
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/service-media /app/service-media

USER adatrack

EXPOSE 8095 8096

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8095/healthz || exit 1

ENTRYPOINT ["/app/service-media"]
