#!/usr/bin/env bash
# ============================================================================
# backup-mysql.sh — Backup harian MySQL (Phase B4, GAP #5).
#
# Dump seluruh database adatrack_gps_* (master + company) menjadi gzip ter-
# timestamp di BACKUP_DIR, lalu hapus salinan lokal lebih lama dari
# RETAIN_DAYS. Untuk offsite: sinkronkan BACKUP_DIR ke S3/GCS/NAS (contoh
# di docs/HIGH_AVAILABILITY.md §4).
#
# Pakai:
#   ./backup-mysql.sh                     # backup semua DB
#   ./backup-mysql.sh adatrack_gps_master    # backup satu DB saja
#
# Cron harian (contoh 01:00):
#   0 1 * * * cd /path/backend/scripts && ./backup-mysql.sh >> /var/log/adatrack-backup.log 2>&1
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"
# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && set -a && . "$ENV_FILE" && set +a

BACKUP_DIR="${BACKUP_DIR:-$SCRIPT_DIR/../backups}"
RETAIN_DAYS="${BACKUP_RETAIN_DAYS:-14}"
TS="$(date +%Y%m%d_%H%M%S)"
OUT="$BACKUP_DIR/$TS"
mkdir -p "$OUT"

MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3307}"
MYSQL_USER="${MYSQL_USER:-root}"
export MYSQL_PWD="${MYSQL_ROOT_PASSWORD:-}"

if [ -z "${MYSQL_PWD:-}" ]; then
  echo "ERROR: MYSQL_ROOT_PASSWORD tidak tersedia (.env)" >&2
  exit 1
fi

mapfile -t DBS < <(docker exec mysql mysql -uroot -p"$MYSQL_PWD" -N -e 'SHOW DATABASES;' 2>/dev/null | grep '^adatrack_gps_' || true)

if [ $# -gt 0 ]; then
  DBS=("$@")
fi

echo "[backup] mulai $TS → $OUT (DB: ${DBS[*]:-none})"
for db in "${DBS[@]}"; do
  echo "[backup] dumping $db ..."
  docker exec mysql sh -c "mysqldump -uroot -p\"\$MYSQL_ROOT_PASSWORD\" \
    --single-transaction --routines --triggers --events \
    --set-gtid-purged=OFF '$db'" 2>/dev/null | gzip > "$OUT/${db}.sql.gz"
  SIZE=$(du -h "$OUT/${db}.sql.gz" | cut -f1)
  echo "[backup]   OK ($SIZE)"
done

# Checksum utk verifikasi integritas saat restore.
( cd "$OUT" && sha256sum ./*.sql.gz > SHA256SUMS ) || true

echo "[cleanup] hapus backup lokal > ${RETAIN_DAYS} hari ..."
find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -mtime "+$RETAIN_DAYS" -exec rm -rf {} \; 2>/dev/null || true

echo "[backup] SELESAI → $OUT"
