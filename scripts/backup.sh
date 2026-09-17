#!/bin/bash
set -e

BACKUP_DIR="./backups"
DB_CONTAINER="adatrack_postgres"
DB_USER="adatrack_local"
DB_NAME="adatrack_gps_master"
DATE=$(date +%Y%m%d_%H%M%S)

mkdir -p ${BACKUP_DIR}
echo "Starting database backup per schema..."

SCHEMAS=$(docker exec -t ${DB_CONTAINER} psql -U ${DB_USER} -d ${DB_NAME} -t -c "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'adatrack_gps_%';")

for schema in $SCHEMAS; do
  schema=$(echo $schema | tr -d '\r')
  if [ -z "$schema" ]; then continue; fi
  BACKUP_FILE="${BACKUP_DIR}/${schema}_backup_${DATE}.sql.gz"
  echo "Backing up schema ${schema} to ${BACKUP_FILE}"
  docker exec -t ${DB_CONTAINER} pg_dump -U ${DB_USER} -d ${DB_NAME} -n ${schema} | gzip > ${BACKUP_FILE}
  sha256sum ${BACKUP_FILE} > ${BACKUP_FILE}.sha256
done

echo "Cleaning up backups older than 14 days..."
find ${BACKUP_DIR} -name "*.sql.gz" -mtime +14 -exec rm {} \;
find ${BACKUP_DIR} -name "*.sha256" -mtime +14 -exec rm {} \;

echo "Backup completed successfully."
