# ======================================================================
# Dockerfile — worker-alert (B3). Deteksi alert & geofence.
# B0 FIX: build context WAJIB `backend/` (monorepo). Jalankan dari `backend/`:
#   docker build -f services/worker-alert/Dockerfile -t adatrack-worker-alert .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/worker-alert
ENV CGO_ENABLED=0
RUN go build -o /out/worker-alert .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/worker-alert .
# metrics/healthz port (ALERT_METRICS_ADDR default :8093)
EXPOSE 8093
CMD ["./worker-alert"]