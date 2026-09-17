#!/bin/bash
set -e

if [ -z "$1" ]; then
  echo "Usage: ./restore.sh <backup_file.sql.gz>"
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

echo "Extracting schema name from backup file..."
SCHEMA_NAME=$(basename $BACKUP_FILE | awk -F'_backup_' '{print $1}')

echo "Restoring schema ${SCHEMA_NAME}..."
zcat ${BACKUP_FILE} | docker exec -i ${DB_CONTAINER} psql -U ${DB_USER} -d ${DB_NAME}

echo "Verifying row count for ${SCHEMA_NAME}.th_telemetry_logs..."
docker exec -t ${DB_CONTAINER} psql -U ${DB_USER} -d ${DB_NAME} -c "SELECT count(*) FROM ${SCHEMA_NAME}.th_telemetry_logs;"

echo "Restore completed successfully."
