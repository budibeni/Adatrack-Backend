# ======================================================================
# Dockerfile — ingestion-tcp (B1). TCP multi-protokol : GT06 + Teltonika + TK103.
# B0 FIX: build context WAJIB `backend/` (monorepo) agar `replace
# ajb_gps/internal => ../../internal` & subdirektori controllers/models
# terbawa. Jalankan dari `backend/`:
#   docker build -f services/ingestion-tcp/Dockerfile -t adatrack-ingestion-tcp .
# ======================================================================
FROM golang:1.25-alpine AS builder
WORKDIR /repo
COPY . .
WORKDIR /repo/services/ingestion-tcp
ENV CGO_ENABLED=0
# Cache mount: modul Go & build cache persisten lintas retry (tahan network
# flaky — retry melanjutkan download dari titik terakhir, tidak dari nol).
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o /out/ingestion-tcp .

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/ingestion-tcp .
# TCP ingestion ports: GT06 (9000), Teltonika (9001), TK103 (9002)
EXPOSE 9000 9001 9002
CMD ["./ingestion-tcp"]
