#!/usr/bin/env bash
# ============================================================================
# backup-redis.sh — Redis snapshot backup best-effort (B4, PRD §12).
# Triggers BGSAVE, waits for the RDB, copies dump.rdb + AOF (when present)
# into backups/redis-<stamp>/ with SHA256SUMS. Keeps the newest 3 snapshots.
# Usage: scripts/backup-redis.sh [backup_dir]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
if [[ -f "$ROOT/.env.${COMPOSE_VARIANT:-local}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$ROOT/.env.${COMPOSE_VARIANT:-local}"
  set +a
fi

BACKUP_DIR="${1:-$ROOT/backups}"
RHOST="${REDIS_HOST:-127.0.0.1}"
if [[ "$RHOST" == "redis" ]]; then RHOST="127.0.0.1"; fi
RPORT="${HOST_REDIS_PORT:-6380}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
DEST="$BACKUP_DIR/redis-$STAMP"
mkdir -p "$DEST"

if [[ -n "${REDIS_PASSWORD:-}" ]]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi

echo "backup-redis: BGSAVE on $RHOST:$RPORT"
redis-cli -h "$RHOST" -p "$RPORT" BGSAVE
for _ in $(seq 1 30); do
  if [[ "$(redis-cli -h "$RHOST" -p "$RPORT" LASTSAVE)" != "" ]]; then sleep 1; fi
  # Poll until no background save in progress.
  if ! redis-cli -h "$RHOST" -p "$RPORT" INFO persistence 2>/dev/null | grep -q "rdb_bgsave_in_progress:1"; then
    break
  fi
done

CID="${REDIS_CONTAINER:-adatrack_redis}"
docker cp "$CID:/data/dump.rdb" "$DEST/dump.rdb" 2>/dev/null || echo "backup-redis: WARN dump.rdb copy failed (dir persistence may differ)"
docker cp "$CID:/data/appendonlydir" "$DEST/appendonlydir" 2>/dev/null || true
(cd "$DEST" && sha256sum dump.rdb > SHA256SUMS 2>/dev/null || echo "backup-redis: no dump.rdb captured")

# Keep newest 3 redis snapshots.
ls -1d "$BACKUP_DIR"/redis-20* 2>/dev/null | sort | head -n -3 | while read -r d; do
  [[ -n "$d" ]] && { echo "backup-redis: pruning $d"; rm -rf "$d"; }
done

echo "backup-redis: done ($DEST)"
