# Build phase
FROM golang:alpine AS builder

WORKDIR /app
# Enable Go modules workspace
ENV CGO_ENABLED=0 GOOS=linux

# Copy the entire backend monorepo to satisfy go.work and internal dependencies
COPY . .

# Build the specific service
RUN go build -o /app/bin/service ./services/service-monitor/main.go

# Runtime phase
FROM alpine:latest

# Add timezone data, CA certs, and docker cli
RUN apk --no-cache add ca-certificates tzdata docker-cli

WORKDIR /app
# Copy the compiled binary
COPY --from=builder /app/bin/service /app/service

# Copy database migrations and scripts
COPY database /app/database
COPY scripts /app/scripts

# Expose required ports
EXPOSE 8085

# Start the service
CMD ["/app/service"]
