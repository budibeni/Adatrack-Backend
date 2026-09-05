# ======================================================================
# Dockerfile — worker-persistence (B1). Batch insert ke MySQL.
# B0 FIX: build context WAJIB `backend/` (monorepo). Jalankan dari `backend/`:
#   docker build -f services/worker-persistence/Dockerfile -t adatrack-worker-persistence .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/worker-persistence
ENV CGO_ENABLED=0
# Cache mount: modul Go & build cache persisten lintas retry (tahan network
# flaky — retry melanjutkan download dari titik terakhir, tidak dari nol).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o /out/worker-persistence .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/worker-persistence .
# metrics/healthz port (PERSISTENCE_METRICS_ADDR default :8091)
EXPOSE 8091
CMD ["./worker-persistence"]