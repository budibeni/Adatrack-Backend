# ============================================================================
# foundation-check — Dockerfile (build context: backend/)
# ============================================================================
# Diagnostic service that proves the B0 wiring (PostgreSQL + Redis + NATS +
# tenant routing). Useful in CI/deploy smoke tests: `docker run ... -once`.
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY internal/ ./internal/
COPY services/foundation-check/ ./services/foundation-check/

WORKDIR /src/services/foundation-check
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/foundation-check .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app

COPY database/ ./database/
COPY --from=build /out/foundation-check /app/foundation-check

USER adatrack

EXPOSE 8093

HEALTHCHECK --interval=15s --timeout=5s --start-period=15s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8093/healthz || exit 1

ENTRYPOINT ["/app/foundation-check"]