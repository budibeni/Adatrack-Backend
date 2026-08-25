#!/usr/bin/env bash
# ============================================================================
# Multi-Tenant Init — MySQL dev (PERTAMA kali volume dibikin saja)
# ============================================================================#
# Mount yang dibutuhkan (lihat deployments/docker-compose.yml):
#   .:        /docker-entrypoint-initdb.d (proyek ini)
#   ../migrations -> /db/migrations
#   ../seed       -> /db/seed
# ============================================================================
set -euo pipefail

# Pakai soket (bukan TCP): server temporer saat init hanya listen via socket, dan
# .env membawa MYSQL_HOST=127.0.0.1 yang memaksa client ke TCP (ERROR 2003).
# Pola ini persis seperti docker-entrypoint.sh sendiri: --protocol=socket
# + -hlocalhost + --socket=<path> → aman di semua env dev/prod.
mysql_cmd=( mysql -uroot -p"${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD required}" \
	--protocol=socket -hlocalhost --socket=/var/run/mysqld/mysqld.sock )

MASTER_DB="${MASTER_DB_NAME:-adatrack_gps_master}"
PREFIX="${COMPANY_DB_PREFIX:-adatrack_gps_}"
DECLARED_DBS=("${PREFIX}default")

log() { echo "[multitenant-init] $*"; }

apply_dir() { # $1=dbName  $2=dir
  local db="$1" dir="$2" f
  for f in "${dir}"/*.sql; do
    [ -f "$f" ] || continue
    log "migrate ${db} < ${f}"
    "${mysql_cmd[@]}" "${db}" < "$f"
  done
}

# 1) Database container (master + default)
"${mysql_cmd[@]}" -e "CREATE DATABASE IF NOT EXISTS \`${MASTER_DB}\` CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;"
for db in "${DECLARED_DBS[@]}"; do
  "${mysql_cmd[@]}" -e "CREATE DATABASE IF NOT EXISTS \`${db}\` CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;"
done

# 2) Migration schema
apply_dir "${MASTER_DB}" /db/migrations/master
for db in "${DECLARED_DBS[@]}"; do
  apply_dir "${db}" /db/migrations/company
done

# 2b) Master reference data (countries/provinces/cities/districts/subdistricts)
#     — dihasilkan oleh backend/database/tools/genregions dari sumber data real.
if [ -d /db/seed/reference ]; then
  for f in /db/seed/reference/*.sql; do
    [ -f "$f" ] || continue
    log "seed reference < $(basename "$f")"
    "${mysql_cmd[@]}" "${MASTER_DB}" < "$f"
  done
fi

# 3) Seed dev data
log "seed master < master_seed.sql"
"${mysql_cmd[@]}" "${MASTER_DB}" < /db/seed/master_seed.sql
for db in "${DECLARED_DBS[@]}"; do
  log "seed ${db} < company_seed.sql"
  "${mysql_cmd[@]}" "${db}" < /db/seed/company_seed.sql
done

# 4) Aplikasi DB user — granted untuk master + semua DB prefix (termasuk tenant baru)
if [ -n "${MYSQL_USER:-}" ] && [ -n "${MYSQL_PASSWORD:-}" ]; then
  "${mysql_cmd[@]}" -e "CREATE USER IF NOT EXISTS '${MYSQL_USER}'@'%' IDENTIFIED BY '${MYSQL_PASSWORD}';"
  "${mysql_cmd[@]}" -e "GRANT ALL PRIVILEGES ON \`${MASTER_DB}\`.* TO '${MYSQL_USER}'@'%';"
  "${mysql_cmd[@]}" -e "GRANT ALL PRIVILEGES ON \`${PREFIX}%\`.* TO '${MYSQL_USER}'@'%';"
  "${mysql_cmd[@]}" -e "FLUSH PRIVILEGES;"
fi

log "multi-tenant init selesai."