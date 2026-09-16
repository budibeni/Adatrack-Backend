#!/bin/bash
set -e
echo "Building Go Migrate Tool..."
cd tools/migrate
go mod tidy
go build -o db-migrate main.go

echo "Waiting for Database..."
for i in {1..10}; do
  if docker exec -i adatrack_postgres_local pg_isready -U adatrack_local -d adatrack_gps_master; then
    echo "DB is ready!"
    break
  fi
  sleep 2
done

echo "Running golang-migrate..."
./db-migrate

echo "Applying Seed Data..."
cat ../../database/init-pg/03_seed_master.sql | docker exec -i adatrack_postgres_local psql -U adatrack_local -d adatrack_gps_master

echo "Migration pipeline complete!"
