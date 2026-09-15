# ============================================================================
# worker-live — Dockerfile (build context: backend/)
# ============================================================================
FROM golang:1.25-alpine AS build

WORKDIR /src

COPY internal/ ./internal/
COPY services/worker-live/ ./services/worker-live/

WORKDIR /src/services/worker-live
RUN go mod download && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/worker-live .

FROM alpine:3.20 AS runtime

RUN apk add --no-cache ca-certificates tzdata wget && \
    adduser -D -u 10001 adatrack

WORKDIR /app
COPY --from=build /out/worker-live /app/worker-live

USER adatrack

EXPOSE 8091

HEALTHCHECK --interval=15s --timeout=5s --start-period=15s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8091/healthz || exit 1

ENTRYPOINT ["/app/worker-live"]