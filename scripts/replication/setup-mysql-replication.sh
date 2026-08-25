#!/usr/bin/env bash
# ============================================================================
# setup-mysql-replication.sh — Pasang replikasi GTID PRIMARY → REPLICA
# (docs/HIGH_AVAILABILITY.md §7 langkah 3).
#
# Asumsi (rehearsal 1-host): primary = container "mysql", replica =
# container "mysql_replica" dari backend/deployments/docker-compose.ha.yml.
#
# Pakai:
#   ./setup-mysql-replication.sh [primary_host:port]
#   default primary_host:port = host.docker.internal:$MYSQL_PORT
#
# CATATAN PERBAIKAN 2026-08-25:
#   1) docker exec kini PAKAI -i untuk semua SQL ber-heredoc — tanpa -i,
#      stdin tidak diteruskan sehingga seluruh langkah SQL diam-diam no-op
#      (replika tampak kosong padahal skrip exit 0).
#   2) Nilai .env dibaca per-key (env_get), BUKAN di-source mentah — nilai
#      yang memuat '|' dsb. membuat bash mengeksekusi sisa baris.
#   3) Dump diverifikasi ukurannya (>10KB) sebelum dipakai men-seed replika.
# ============================================================================
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
env_get() { # env_get KEY [default] — OS env menang atas backend/.env
local v=""
v="$(grep -E "^${1}=" "$SCRIPT_DIR/.env" 2>/dev/null | tail -n1 | cut -d= -f2- || true)"
if printenv "$1" >/dev/null 2>&1; then v="$(printenv "$1")"; fi
echo "${v:-${2:-}}"
}
MYSQL_ROOT_PASSWORD="$(env_get MYSQL_ROOT_PASSWORD)"
[ -n "$MYSQL_ROOT_PASSWORD" ] || { echo "MYSQL_ROOT_PASSWORD wajib ada (.env/env)"; exit 1; }
MYSQL_PORT="$(env_get MYSQL_PORT 3307)"
export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"

SRC="${1:-host.docker.internal:$MYSQL_PORT}"
REPL_USER="${REPLICATION_USER:-repl}"
REPL_PASS="${REPLICATION_PASSWORD:-repl-pass-change-me}"

echo "[1/4] Primary: buat user replikasi + cek GTID ..."
docker exec -i mysql sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD"' <<'SQL'
CREATE USER IF NOT EXISTS 'repl'@'%' IDENTIFIED BY 'repl-pass-change-me';
GRANT REPLICATION SLAVE ON *.* TO 'repl'@'%';
FLUSH PRIVILEGES;
SELECT @@global.gtid_mode AS gtid_mode, @@global.server_uuid AS uuid;
SQL

echo "[2/4] Primary: dump konsisten (--single-transaction, --source-data) ..."
DUMP=/tmp/repl_seed.sql.gz
docker exec mysql sh -c 'mysqldump -uroot -p"$MYSQL_ROOT_PASSWORD" --all-databases --single-transaction --set-gtid-purged=ON --source-data=2 --triggers --routines --events' | gzip > "$DUMP"
SIZE=$(stat -c%s "$DUMP")
echo "dump: $SIZE bytes"
[ "$SIZE" -gt 10000 ] || { echo "FATAL: dump terlalu kecil ($SIZE B) — mysqldump gagal"; exit 1; }

echo "[3/4] Replica: impor seed ..."
docker exec -i mysql_replica sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD"' <<'SQL'
STOP REPLICA;
RESET REPLICA ALL;
SET GLOBAL read_only = ON;
SQL
gunzip -c "$DUMP" | docker exec -i mysql_replica sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD"'
# Dump meng-INSERT baris grants (mysql.db) via SQL murni — ACL in-memory
# perlu di-refresh agar user aplikasi langsung boleh memakai DB tenant:
docker exec -i mysql_replica sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "FLUSH PRIVILEGES"'

echo "[4/4] Replica: CHANGE REPLICATION SOURCE TO $SRC ..."
docker exec -i mysql_replica sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD"' <<SQL
CHANGE REPLICATION SOURCE TO
  SOURCE_HOST='${SRC%%:*}',
  SOURCE_PORT=${SRC##*:},
  SOURCE_USER='repl',
  SOURCE_PASSWORD='repl-pass-change-me',
  SOURCE_AUTO_POSITION=1,
  GET_SOURCE_PUBLIC_KEY=1;
START REPLICA;
SHOW REPLICA STATUS\\G
SQL

echo "[setup] SELESAI — verifikasi dengan ./replication-status.sh"
