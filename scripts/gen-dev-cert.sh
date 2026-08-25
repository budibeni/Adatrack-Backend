#!/usr/bin/env bash
# gen-dev-cert.sh — generates a self-signed TLS cert/key for local dev HTTPS/WSS.
# DO NOT use in production (use certbot / ACM / secret manager — PRD §8.2).
set -euo pipefail
OUT_DIR="${1:-$(dirname "$0")/certs}"
mkdir -p "$OUT_DIR"
CERT="$OUT_DIR/dev.crt"
KEY="$OUT_DIR/dev.key"
if [ -f "$CERT" ] && [ -f "$KEY" ]; then
  echo "dev certs already exist at $CERT / $KEY (remove to regenerate)"
  exit 0
fi
CN="${CERT_CN:-127.0.0.1}"
openssl req -x509 -newkey rsa:2048 -nodes -days 825 \
  -keyout "$KEY" -out "$CERT" \
  -subj "/CN=${CN}" \
  -addext "subjectAltName=DNS:localhost,DNS:${CN},IP:127.0.0.1" \
  >/dev/null 2>&1
chmod 600 "$KEY"
chmod 644 "$CERT"
echo "generated dev TLS cert: $CERT"
echo "generated dev TLS key : $KEY"
echo "Run services with: TLS_ENABLED=true TLS_CERT_FILE=$CERT TLS_KEY_FILE=$KEY"
