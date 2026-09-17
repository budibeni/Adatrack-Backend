#!/bin/bash
set -e

BACKUP_DIR="./backups"
DB_CONTAINER="adatrack_postgres"
DB_USER="adatrack_local"
DB_NAME="adatrack_gps_master"
DATE=$(date +%Y%m%d_%H%M%S)
BACKUP_FILE="${BACKUP_DIR}/db_backup_${DATE}.sql"

mkdir -p ${BACKUP_DIR}

echo "Starting database backup..."
docker exec -t ${DB_CONTAINER} pg_dump -U ${DB_USER} -d ${DB_NAME} -F c > ${BACKUP_FILE}

echo "Generating checksum..."
sha256sum ${BACKUP_FILE} > ${BACKUP_FILE}.sha256

echo "Backup completed successfully at ${BACKUP_FILE}"
