# ======================================================================
# Dockerfile — api-vehicle (B3). CRUD vehicle (Gin).
# B0 FIX: build context WAJIB `backend/` (monorepo). Jalankan dari `backend/`:
#   docker build -f services/api-vehicle/Dockerfile -t adatrack-api-vehicle .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/api-vehicle
ENV CGO_ENABLED=0
# Cache mount: modul Go & build cache persisten lintas retry (tahan network
# flaky — retry melanjutkan download dari titik terakhir, tidak dari nol).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o /out/api-vehicle .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/api-vehicle .
# REST API (API_VEHICLE_HTTP_ADDR default :8081)
EXPOSE 8081
CMD ["./api-vehicle"]
