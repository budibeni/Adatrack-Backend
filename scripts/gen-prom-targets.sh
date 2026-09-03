#!/usr/bin/env bash
# gen-prom-targets.sh (B4 audit 2026-08-31) — generate file_sd targets untuk
# Prometheus menunjuk service Go yang berjalan DI HOST dev.
#
# LATAR: di Docker Desktop + WSL2, `host.docker.internal` TIDAK merutekan ke
# userland distro WSL tempat service jalan (connection refused), sementara IP
# distro WSL berubah setiap restart Windows/WSL. Solusi: file_sd — file targets
# di-generate ulang oleh script ini (mis. dipanggil start-services.sh / cron /
# manual) sehingga tidak perlu edit prometheus.yml tiap restart.
#
# Pemakaian:
#   bash scripts/gen-prom-targets.sh            # pakai IP `hostname -I` pertama
#   bash scripts/gen-prom-targets.sh 10.0.0.5   # paksa IP tertentu
#
# Port mengikuti backend/.env: ingestion 8090, persistence 8091, live 8092,
# alert 8093, websocket metrics 9091 (override dev; default .env 9090 bentrok
# dgn Prometheus), api-vehicle 8081.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/monitoring/targets"
FILE="$DIR/adatrack-services.json"
mkdir -p "$DIR"

IP="${1:-}"
if [[ -z "$IP" ]]; then
  IP="$(hostname -I | awk '{print $1}')"
fi
if [[ -z "$IP" || "$IP" == 127.0.0.1* ]]; then
  echo "ERROR: IP WSL tidak valid: '$IP'" >&2
  exit 1
fi

cat > "$FILE" <<EOF
[
  {
    "targets": [
      "$IP:8090",
      "$IP:8091",
      "$IP:8092",
      "$IP:8093",
      "$IP:9091",
      "$IP:8081"
    ],
    "labels": {
      "job": "adatrack-services"
    }
  }
]
EOF
echo "targets ditulis: $FILE (IP=$IP)"