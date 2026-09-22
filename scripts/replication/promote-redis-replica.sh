#!/usr/bin/env bash
# ============================================================================
# promote-redis-replica.sh — promote / fail-back replika Redis (PRD §13)
# ============================================================================
# Replika DB sengaja TIDAK punya skrip promote (keputusan 2026-08-25: replika
# bukan failover, prosedur manual PG: `SELECT pg_promote()` + rebuild primary
# lama lewat pg_basebackup). Redis MEMPERTAHANKAN promote + drill karena perannya
# jalur pemulihan cepat state live (target cadangan ≤ 5 menit).
#
# Usage:
#   scripts/replication/promote-redis-replica.sh promote  --yes
#   scripts/replication/promote-redis-replica.sh failback --yes
#
# promote  : REPLICAOF NO ONE  → replika jadi primary (tulis diterima)
# failback : REPLICAOF redis 6379 → kembali jadi replika (full resync)
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}" >/dev/null

ACTION="${1:-}"
CONFIRM="${2:-}"
REDIS_REPLICA_PORT="${HOST_REDIS_REPLICA_PORT:-6381}"
REDIS_REPLICA_HOST="${REDIS_REPLICA_HOST:-127.0.0.1}"
PRIMARY_HOST_FROM_REPLICA="${REDIS_PRIMARY_HOST:-redis}"
PRIMARY_PORT="${REDIS_PORT:-6379}"

if [[ "$ACTION" != "promote" && "$ACTION" != "failback" ]]; then
  echo "usage: $0 promote|failback --yes" >&2
  exit 2
fi
if [[ "$CONFIRM" != "--yes" ]]; then
  echo "promote-redis-replica: operasi ini mengubah topologi replikasi." >&2
  echo "jalankan ulang dengan --yes bila memang disengaja (mis. di dalam drill)." >&2
  exit 2
fi

rcli() { redis-cli -h "$REDIS_REPLICA_HOST" -p "$REDIS_REPLICA_PORT" "$@"; }

case "$ACTION" in
  promote)
    rcli REPLICAOF NO ONE >/dev/null
    role="$(rcli INFO replication | grep -E '^role:' | tr -d '\r')"
    echo "promote: $role"
    [[ "$role" == 'role:master' ]] || { echo 'promote GAGAL (role bukan master)' >&2; exit 1; }
    ;;
  failback)
    rcli REPLICAOF "$PRIMARY_HOST_FROM_REPLICA" "$PRIMARY_PORT" >/dev/null
    for _ in $(seq 1 30); do
      link="$(rcli INFO replication | grep -E '^master_link_status:' | tr -d '\r')"
      [[ "$link" == 'master_link_status:up' ]] && break
      sleep 1
    done
    echo "failback: ${link:-master_link_status:tidak diketahui}"
    [[ "${link:-}" == 'master_link_status:up' ]] || { echo 'failback GAGAL (link belum up)' >&2; exit 1; }
    ;;
esac
