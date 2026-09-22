# ============================================================================
# worker-alert — Dockerfile (build context: backend/)
# ============================================================================
# Engine alarm (PRD §10.3, fase B3): detektor offline, fuel drop, geofence,
# route deviation, speed band + notifikasi (WS/email/SMS) + audit.
#
# Sesuai PRD §14.5 step 1: database/ (migrasi + seed) ikut ke dalam image supaya
# artefak migrasi seversi dengan kode dan MIGRATE_ON_BOOT bisa berjalan tanpa
# akses repo.
#
# Multi-stage: static build (CGO disabled) -> runtime alpine non-root.
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

# Shared module dulu supaya layer cache lebih efektif.
COPY internal/ ./internal/
COPY services/worker-alert/ ./services/worker-alert/

WORKDIR /src/services/worker-alert
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker-alert .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Artefak migrasi + helper wait (§14.5).
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/worker-alert /app/worker-alert

USER adatrack

# /healthz + /metrics (ALERT_METRICS_ADDR, default :8094).
EXPOSE 8094

HEALTHCHECK --interval=15s --timeout=5s --start-period=25s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8094/healthz || exit 1

ENTRYPOINT ["/app/worker-alert"]
