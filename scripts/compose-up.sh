#!/bin/bash
MODE=${1:-local}
echo "Starting compose in $MODE mode..."
docker-compose -f docker-compose.yml -f docker-compose.$MODE.yml up -d
