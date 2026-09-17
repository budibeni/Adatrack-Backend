#!/bin/bash
set -e

if [ -z "$1" ]; then
  echo "Usage: ./restore.sh <backup_file.sql>"
  exit 1
fi

BACKUP_FILE=$1
DB_CONTAINER="adatrack_postgres"
DB_USER="adatrack_local"
DB_NAME="adatrack_gps_master"

if [ ! -f "${BACKUP_FILE}" ]; then
  echo "Backup file not found!"
  exit 1
fi

echo "Verifying checksum..."
sha256sum -c ${BACKUP_FILE}.sha256

echo "Restoring database..."
# Drop and recreate if needed (be careful in production)
docker exec -i ${DB_CONTAINER} pg_restore -U ${DB_USER} -d ${DB_NAME} -c < ${BACKUP_FILE}

echo "Restore completed successfully."
