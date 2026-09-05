# ======================================================================
# Dockerfile — service-websocket (B2). REST API + WebSocket + RBAC.
# B0 FIX: build context WAJIB `backend/` (monorepo). Jalankan dari `backend/`:
#   docker build -f services/service-websocket/Dockerfile -t adatrack-service-websocket .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/service-websocket
ENV CGO_ENABLED=0
# Cache mount: modul Go & build cache persisten lintas retry (tahan network
# flaky — retry melanjutkan download dari titik terakhir, tidak dari nol).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o /out/service-websocket .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/service-websocket .
# HTTP REST + WebSocket (WEBSOCKET_HTTP_ADDR default :8080)
EXPOSE 8080
CMD ["./service-websocket"]