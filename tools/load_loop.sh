#!/bin/bash
cd $(dirname "$0")/loadtest
END_TIME=$(($(date +%s) + 86400))
while [ $(date +%s) -lt $END_TIME ]; do
  echo "$(date): Running 15-minute load cycle (1000 msg/s, 2000 conns)"
  ./loadtest -target "localhost:15000" -conns 2000 -rate 1000 -duration 900
  sleep 5
done
echo "Endurance test completed."
