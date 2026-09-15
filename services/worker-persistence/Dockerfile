# ============================================================================
# worker-persistence — Dockerfile (build context: backend/)
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY internal/ ./internal/
COPY services/worker-persistence/ ./services/worker-persistence/

WORKDIR /src/services/worker-persistence
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker-persistence .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Migrations travel with the image so MIGRATE_ON_BOOT works offline (§14.5).
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/worker-persistence /app/worker-persistence

USER adatrack

EXPOSE 8092

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8092/healthz || exit 1

ENTRYPOINT ["/app/worker-persistence"]