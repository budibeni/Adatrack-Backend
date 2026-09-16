#!/bin/bash
set -e
echo "Starting Database Migration..."
for i in {1..10}; do
  if docker exec -i adatrack_postgres_local pg_isready -U adatrack_local -d adatrack_gps_master; then
    echo "DB is ready!"
    break
  fi
  echo "Waiting for DB..."
  sleep 2
done

echo "Applying Master Migrations 001-013..."
for file in database/migrations/master_pg/*.up.sql; do
  echo "Applying $file..."
  cat "$file" | docker exec -i adatrack_postgres_local psql -U adatrack_local -d adatrack_gps_master
done

echo "Applying Seed Data..."
cat database/init-pg/03_seed_master.sql | docker exec -i adatrack_postgres_local psql -U adatrack_local -d adatrack_gps_master

echo "Migrations completed successfully!"
