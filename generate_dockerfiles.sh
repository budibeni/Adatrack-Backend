#!/bin/bash

# Define services and their exposed ports
declare -A SERVICES=(
  ["api-vehicle"]="8084"
  ["foundation-check"]="8080"
  ["ingestion-tcp"]="8081 5000-5030"
  ["service-websocket"]="8080"
  ["worker-alert"]="8083"
  ["worker-live"]="8082"
  ["worker-persistence"]="8085"
)

for service in "${!SERVICES[@]}"; do
  port="${SERVICES[$service]}"
  cat << DOCKERFILE > "services/$service/Dockerfile"
# Build phase
FROM golang:1.24-alpine AS builder

WORKDIR /app
# Enable Go modules workspace
ENV CGO_ENABLED=0 GOOS=linux

# Copy the entire backend monorepo to satisfy go.work and internal dependencies
COPY . .

# Build the specific service
RUN go build -o /app/bin/service ./services/$service/main.go

# Runtime phase
FROM alpine:latest

# Add timezone data and CA certs
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
# Copy the compiled binary
COPY --from=builder /app/bin/service /app/service

# Copy database migrations and scripts (Required for Coolify pre-deploy hooks - PRD 14.5)
COPY database /app/database
COPY scripts /app/scripts

# Expose required ports
EXPOSE $port

# Start the service
CMD ["/app/service"]
DOCKERFILE
  echo "Created services/$service/Dockerfile"
done
