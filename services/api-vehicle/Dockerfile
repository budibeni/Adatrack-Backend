# Build phase
FROM golang:alpine AS builder

WORKDIR /app
# Enable Go modules workspace
ENV CGO_ENABLED=0 GOOS=linux

# Copy the entire backend monorepo to satisfy go.work and internal dependencies
COPY . .

# Build the specific service
RUN go build -o /app/bin/service ./services/api-vehicle/main.go

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
EXPOSE 8084

# Start the service
CMD ["/app/service"]
