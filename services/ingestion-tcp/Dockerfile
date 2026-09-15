# ============================================================================
# ingestion-tcp — Dockerfile (build context: backend/)
# ============================================================================
# PRD §14.5 step 1: database/ (migrations + seeds) is copied into the image so a
# deploy always carries the migration artifacts of the same commit.
# Multi-stage: static build (CGO disabled) → distroless-ish alpine runtime.
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

# Shared module first (better layer caching).
COPY internal/ ./internal/
COPY services/ingestion-tcp/ ./services/ingestion-tcp/

WORKDIR /src/services/ingestion-tcp
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ingestion-tcp .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

# Migration artifacts travel with the image (§14.5) + helper scripts.
COPY database/ ./database/
COPY scripts/pg-wait.sh ./scripts/pg-wait.sh
COPY --from=build /out/ingestion-tcp /app/ingestion-tcp

USER adatrack

# GT06 + Teltonika listeners and the metrics/health endpoint.
EXPOSE 9003 9011 8090

# /healthz is the readiness signal (PostgreSQL + NATS + tenant pools).
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8090/healthz || exit 1

ENTRYPOINT ["/app/ingestion-tcp"]