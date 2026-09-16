#!/bin/bash
mkdir -p deployments database/init-pg database/migrations/master_pg database/migrations/company_pg
mkdir -p internal/config internal/logger internal/metrics internal/natsclient internal/dbclient internal/redclient internal/tenant
mkdir -p scripts
mkdir -p services/ingestion-tcp/controllers services/ingestion-tcp/models
mkdir -p services/worker-live/controllers services/worker-live/models
mkdir -p services/worker-persistence/controllers services/worker-persistence/models

# Docker Compose files
touch docker-compose.yml docker-compose.local.yml docker-compose.coolify.yml
touch .env.local .env.coolify

# Makefile
touch Makefile

# Go mod (we'll just create the file manually since go is not found)
echo "module adatrack" > go.mod
echo "go 1.21" >> go.mod

