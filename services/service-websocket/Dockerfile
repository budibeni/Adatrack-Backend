# ============================================================================
# service-websocket — Dockerfile (build context: backend/)
# ============================================================================
# Serves REST + WebSocket + /healthz + /metrics on HTTP_ADDR (8082, PRD §14.2).
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
COPY --from=build /out/service-websocket /app/service-websocket

USER adatrack

EXPOSE 8082

HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -q -O - http://127.0.0.1:8082/healthz || exit 1

ENTRYPOINT ["/app/service-websocket"]