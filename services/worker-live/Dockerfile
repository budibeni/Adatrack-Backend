# ======================================================================
# Dockerfile — worker-live (B1). Live-state writer Redis.
# B0 FIX: build context WAJIB `backend/` (monorepo) agar `replace
# ajb_gps/internal => ../../internal` & subdirektori controllers/models
# terbawa. Jalankan dari `backend/`:
#   docker build -f services/worker-live/Dockerfile -t adatrack-worker-live .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/worker-live
ENV CGO_ENABLED=0
RUN go build -o /out/worker-live .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/worker-live .
# metrics/healthz port (LIVE_METRICS_ADDR default :8092)
EXPOSE 8092
CMD ["./worker-live"]