#!/bin/bash
set -e
MODE=${1:-local}
echo "Starting infra in $MODE mode..."
docker-compose --env-file .env.$MODE -f docker-compose.$MODE.yml up -d
