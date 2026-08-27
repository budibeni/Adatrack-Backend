# ======================================================================
# Dockerfile — service-media (B5b, PRD v1.3.0 Module 8). Dashcam event media.
# B0 FIX: build context WAJIB `backend/` (monorepo). Jalankan dari `backend/`:
#   docker build -f services/service-media/Dockerfile -t adatrack-service-media .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/service-media
ENV CGO_ENABLED=0
RUN go build -o /out/service-media .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/service-media .
# REST API (SERVICE_MEDIA_HTTP_ADDR default :8095)
EXPOSE 8095
CMD ["./service-media"]
