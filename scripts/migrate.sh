#!/bin/bash
set -e
echo "Starting Database Migration..."

# Retry loop to wait for DB
for i in {1..10}; do
  if docker exec -i adatrack_postgres_local pg_isready -U adatrack_admin -d adatrack_gps_master; then
    echo "DB is ready!"
    break
  fi
  echo "Waiting for DB to be ready..."
  sleep 2
done

echo "Applying Master Migrations 001-013..."
# Normally we'd use a tool like golang-migrate to handle the ledger tracking, 
# but for demonstration we'll just apply them directly.
for file in database/migrations/master_pg/*.up.sql; do
  echo "Applying $file..."
  cat "$file" | docker exec -i adatrack_postgres_local psql -U adatrack_admin -d adatrack_gps_master
done

echo "Applying seed data..."
cat database/init-pg/03_seed_data.sql | docker exec -i adatrack_postgres_local psql -U adatrack_admin -d adatrack_gps_master

echo "Migrations completed successfully!"
