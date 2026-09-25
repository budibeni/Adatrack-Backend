#!/bin/bash
cd $(dirname "$0")/..
export PATH=$PATH:/usr/local/go/bin

killall ingestion-tcp loadtest 2>/dev/null
sleep 2

# Start server
echo "Starting ingestion-tcp..."
cd services/ingestion-tcp
go build .
set -a; source ../../.env.local; set +a
nohup ./ingestion-tcp > ../../endurance_ingestion.log 2>&1 &
SERVER_PID=$!
cd ../..

echo "Waiting for server to bind ports..."
sleep 5

# Start loadtester
echo "Starting load test loop..."
cd tools/loadtest
go build -o loadtest main.go
nohup bash -c '
while true; do
  echo "$(date): Starting 15-minute load cycle (1000 msg/s, 2000 conns)"
  ./loadtest -target "localhost:15000" -conns 2000 -rate 1000 -duration 900
  sleep 5
done
' > ../../endurance_load.log 2>&1 &
TESTER_PID=$!

echo "Endurance Test running. PIDs: Server=$SERVER_PID Tester=$TESTER_PID"
