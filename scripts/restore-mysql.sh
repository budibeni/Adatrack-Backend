#!/usr/bin/env bash
# ============================================================================
# restore-mysql.sh — Uji/drill restore hasil backup-mysql.sh (GAP #5).
#
# "Backup yang tidak pernah diuji bukan backup." Skrip ini me-restore satu
# file dump .sql.gz ke database tujuan (default: prefix restore_test_) lalu
# mencetak jumlah baris per tabel untuk diverifikasi manual.
#
# Pakai:
#   ./restore-mysql.sh <dump.sql.gz> [target_db]
#   contoh drill:
#     ./restore-mysql.sh ../backups/20260823_xxx/adatrack_gps_master.sql.gz adatrack_gps_restore_test
# ============================================================================
set -euo pipefail

DUMP="${1:?pakai: $0 <dump.sql.gz> [target_db]}"
TARGET_DB="${2:-adatrack_gps_restore_test}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"
# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && set -a && . "$ENV_FILE" && set +a
export MYSQL_PWD="${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD dibutuhkan}"

[ -f "$DUMP" ] || { echo "ERROR: dump tidak ditemukan: $DUMP" >&2; exit 1; }

echo "[restore] verifikasi checksum ..."
DIR=$(dirname "$DUMP")
if [ -f "$DIR/SHA256SUMS" ]; then
  ( cd "$DIR" && grep "$(basename "$DUMP")" SHA256SUMS | sha256sum -c - ) || {
    echo "ERROR: checksum mismatch!" >&2; exit 1;
  }
fi

echo "[restore] drop/create $TARGET_DB ..."
docker exec mysql mysql -uroot -p"$MYSQL_PWD" -e "
  DROP DATABASE IF EXISTS \`$TARGET_DB\`;
  CREATE DATABASE \`$TARGET_DB\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" 2>/dev/null

echo "[restore] mengimpor $(basename "$DUMP") → $TARGET_DB ..."
gunzip -c "$DUMP" | docker exec -i mysql mysql -uroot -p"$MYSQL_PWD" "$TARGET_DB" 2>/dev/null

echo "[restore] ringkasan isi:"
docker exec mysql mysql -uroot -p"$MYSQL_PWD" -N -e "
  SELECT table_name, table_rows FROM information_schema.tables
  WHERE table_schema='$TARGET_DB' ORDER BY table_name;" 2>/dev/null

echo "[restore] SELESAI — bandingkan row count dgn sumber, lalu:"
echo "  docker exec mysql mysql -uroot -p\$MYSQL_ROOT_PASSWORD -e 'DROP DATABASE \`$TARGET_DB\`;'"
