#!/usr/bin/env bash
# ============================================================================
# promote-redis-replica.sh — FAILOVER Redis: replica → master (runbook §5 #4).
# State live hanya TTL 5 menit — kehilangan ≤5 mnt diterima (HA doc §3).
# ============================================================================
set -euo pipefail
read -rp "Promote redis_replica jadi master? Ketik PROMOTE: " c
[ "$c" = "PROMOTE" ] || { echo "dibatalkan"; exit 1; }
docker exec redis_replica redis-cli REPLICAOF NO ONE
docker exec redis_replica redis-cli INFO replication | grep -E '^role'
echo "[promote] SELESAI."
