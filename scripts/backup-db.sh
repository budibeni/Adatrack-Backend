#!/usr/bin/env bash
# ============================================================================
# backup-db.sh — daily PostgreSQL backup per schema + SHA256 (B4, PRD §12).
# Dumps every adatrack_gps_* schema (master + tenants) with pg_dump (custom
# format, gzip) + SHA256SUMS manifest. Local retention 14 days.
# Usage: scripts/backup-db.sh [backup_dir]
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"
load_variant_env "${COMPOSE_VARIANT:-local}"

BACKUP_DIR="${1:-${BACKUP_DIR:-$ROOT/backups}}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-14}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"

mkdir -p "$BACKUP_DIR/$STAMP"
echo "backup-db: dir=$BACKUP_DIR/$STAMP retention=${RETENTION_DAYS}d"

schemas="$(psql -tA -v ON_ERROR_STOP=1 --no-psqlrc -X -q \
  -c "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'adatrack_gps_%' ORDER BY 1;")"
[[ -n "$schemas" ]] || { echo "backup-db: no adatrack schemas found" >&2; exit 1; }

for s in $schemas; do
  out="$BACKUP_DIR/$STAMP/${s}.dump.gz"
  echo "backup-db: dumping $s"
  pg_dump -Fc -n "$s" | gzip -c > "$out"
done

(cd "$BACKUP_DIR/$STAMP" && sha256sum ./*.dump.gz > SHA256SUMS)
echo "backup-db: manifest:"
cat "$BACKUP_DIR/$STAMP/SHA256SUMS"

# Retention: prune backup dirs older than RETENTION_DAYS (never the newest).
ls -1d "$BACKUP_DIR"/20* 2>/dev/null | sort | head -n -1 | while read -r d; do
  if [[ -n "$d" && "$(find "$d" -maxdepth 0 -mtime +"$RETENTION_DAYS" 2>/dev/null)" != "" ]]; then
    echo "backup-db: pruning $d (>${RETENTION_DAYS}d)"
    rm -rf "$d"
  fi
done

echo "backup-db: done ($STAMP)"
