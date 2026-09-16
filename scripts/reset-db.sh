#!/bin/bash
set -e
echo "Resetting database..."
docker-compose --env-file .env.local -f docker-compose.local.yml down -v
./scripts/compose-up.sh local
echo "Waiting for postgres to be ready..."
sleep 10
echo "Database reset complete."
