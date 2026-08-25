#!/usr/bin/env bash
# ============================================================================
# backup-redis.sh — Snapshot Redis live-state (Phase B4, GAP #5 sekunder).
#
# State Redis = live position (TTL 5 menit) — menurut HIGH_AVAILABILITY §4
# tidak wajib di-backup; skrip ini menyediakan snapshot best-effort utk
# debugging/audit. RPO live-state tetap ditangani replikasi (HA doc §3).
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"
# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && set -a && . "$ENV_FILE" && set +a

BACKUP_DIR="${BACKUP_DIR:-$SCRIPT_DIR/../backups/redis}"
TS="$(date +%Y%m%d_%H%M%S)"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-7}"
mkdir -p "$BACKUP_DIR"

echo "[redis-backup] BGSAVE ..."
docker exec redis redis-cli BGSAVE >/dev/null
# Tunggu BGSAVE selesai (last_bgsave_status ok & bukan in-progress).
for _ in $(seq 1 30); do
  ST=$(docker exec redis redis-cli INFO persistence | grep rdb_bgsave_in_progress | tr -d '\r' | cut -d: -f2)
  [ "$ST" = "0" ] && break
  sleep 1
done

docker cp redis:/data/dump.rdb "$BACKUP_DIR/dump_${TS}.rdb"
echo "[redis-backup] tersimpan: $BACKUP_DIR/dump_${TS}.rdb ($(du -h "$BACKUP_DIR/dump_${TS}.rdb" | cut -f1))"

find "$BACKUP_DIR" -name 'dump_*.rdb' -mtime "+$RETAIN_DAYS" -delete 2>/dev/null || true
echo "[redis-backup] SELESAI"
