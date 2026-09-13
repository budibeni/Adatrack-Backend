#!/bin/sh
# ============================================================================
# Migration entrypoint - runs master migrations + seed
# ============================================================================

set -e

echo "=========================================="
# Wait for postgres to be ready
echo "Waiting for PostgreSQL..."
until pg_isready -h "${POSTGRES_HOST:-postgres}" -p "${POSTGRES_PORT:-5432}" -U "${POSTGRES_USER:-adatrack_gps_user}"; do
  sleep 1
done
echo "PostgreSQL is ready!"

# Run master schema + migrations
echo "Running master schema..."
PGPASSWORD="${POSTGRES_PASSWORD}" psql \
  -h "${POSTGRES_HOST:-postgres}" \
  -p "${POSTGRES_PORT:-5432}" \
  -U "${POSTGRES_USER:-adatrack_gps_user}" \
  -d "${POSTGRES_DB:-adatrack_gps_db}" \
  -v ON_ERROR_STOP=1 \
  <<'EOSQL'
CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
CREATE SCHEMA IF NOT EXISTS adatrack_gps_dev001;
EOSQL

echo "Running master_setup.sql..."
PGPASSWORD="${POSTGRES_PASSWORD}" psql \
  -h "${POSTGRES_HOST:-postgres}" \
  -p "${POSTGRES_PORT:-5432}" \
  -U "${POSTGRES_USER:-adatrack_gps_user}" \
  -d "${POSTGRES_DB:-adatrack_gps_db}" \
  -v ON_ERROR_STOP=1 \
  -f /scripts/init-pg/02_master_setup.sql

# Run company schema for DEV001
echo "Running company_setup.sql for DEV001..."
PGPASSWORD="${POSTGRES_PASSWORD}" psql \
  -h "${POSTGRES_HOST:-postgres}" \
  -p "${POSTGRES_PORT:-5432}" \
  -U "${POSTGRES_USER:-adatrack_gps_user}" \
  -d "${POSTGRES_DB:-adatrack_gps_db}" \
  -v ON_ERROR_STOP=1 \
  -f /scripts/init-pg/03_company_setup.sql 2>/dev/null || true

echo "=========================================="
echo "Migration completed successfully!"
echo "=========================================="
