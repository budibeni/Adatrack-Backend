#!/bin/bash
export PATH=$PATH:/usr/local/go/bin
cd $(dirname "$0")/..

# Start ingestion-tcp in background
cd services/ingestion-tcp
set -a; source ../../.env.local; set +a
nohup ./ingestion-tcp > ../../endurance_ingestion.log 2>&1 &
SERVER_PID=$!
echo "Started ingestion-tcp with PID $SERVER_PID"

cd ../../tools/loadtest
go build -o loadtest main.go
cd ../..

echo "Starting 24h endurance load test loop..."
nohup ./tools/load_loop.sh > endurance_load.log 2>&1 &
TESTER_PID=$!

echo "Endurance test is now running in the background."
echo "You can monitor the logs:"
echo "  tail -f endurance_ingestion.log"
echo "  tail -f endurance_load.log"
echo "To stop early, run: kill $SERVER_PID $TESTER_PID"
