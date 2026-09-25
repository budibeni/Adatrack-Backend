#!/bin/bash
set -e

# Configuration with defaults
HOST="${LOAD_TEST_HOST:-127.0.0.1}"
PORT="${LOAD_TEST_PORT:-15000}"
DEVICES="${LOAD_TEST_DEVICES:-50}"
DURATION="${LOAD_TEST_DURATION:-10s}"
RATE="${LOAD_TEST_RATE:-1.0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=== Adatrack Real TCP Load Test Runner ==="
echo "Host:     $HOST"
echo "Port:     $PORT (GT06 Protocol)"
echo "Devices:  $DEVICES"
echo "Duration: $DURATION"
echo "Rate:     $RATE msg/s per device"
echo "=========================================="

# Ensure Go is in PATH
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin

LOADGEN_BIN="$ROOT_DIR/tools/loadgen/loadgen"
if [ ! -f "$LOADGEN_BIN" ] || [ "$ROOT_DIR/tools/loadgen/main.go" -nt "$LOADGEN_BIN" ]; then
    echo "Building real TCP loadgen tool..."
    (cd "$ROOT_DIR/tools/loadgen" && go build -o loadgen .)
fi

echo "Executing real load test..."
"$LOADGEN_BIN" \
    -host "$HOST" \
    -port "$PORT" \
    -devices "$DEVICES" \
    -duration "$DURATION" \
    -rate "$RATE"
