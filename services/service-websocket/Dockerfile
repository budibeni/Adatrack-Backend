# ============================================================================
# service-websocket — Dockerfile (build context: backend/)
# ============================================================================
# Serves REST + WebSocket + /healthz + /metrics on HTTP_ADDR (8082, PRD §14.2).
#
# PRD §14.5 step 1: database/ (migrasi + seed) WAJIB ikut ke dalam image.
# MIGRATE_ON_BOOT membaca ./database/migrations/{master,company}_pg relatif ke
# WORKDIR, jadi tanpa COPY di bawah service gagal boot dengan
# "migrate: read dir ./database/migrations/master_pg: no such file or directory"
# lalu restart tanpa henti. Fail-fast itu disengaja (jangan pernah jalan dengan
# schema lama) — yang perlu diperbaiki adalah artefak migrasinya ikut ke image.
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY internal/ ./internal/
COPY services/service-websocket/ ./services/service-websocket/

WORKDIR /src/services/service-websocket
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/service-websocket .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Artefak migrasi + helper wait (§14.5 step 1) untuk MIGRATE_ON_BOOT.
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/service-websocket /app/service-websocket

USER adatrack

EXPOSE 8082

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8082/healthz || exit 1

ENTRYPOINT ["/app/service-websocket"]