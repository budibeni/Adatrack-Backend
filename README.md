# AdaTrack Backend

## Overview
AdaTrack is a robust GPS tracking platform. The backend is designed with a microservices architecture in Go, handling high-throughput telemetry ingestion, WebSocket fan-out, and multi-tenant persistence.

## Architecture

The backend consists of several Go microservices decoupled by NATS JetStream:

```
Perangkat GPS (TCP) ──► ingestion-tcp (GT06, Teltonika dll)
                            │  publish telemetry.raw.{imei}
                            ▼
                      NATS JetStream (TELEMETRY, ALERT, NOTIFY, MEDIA, DLQ)
                            │                 │
              worker-persistence        worker-live ──► Redis (state) + Postgres (trips/odometer)
              (batch insert, DLQ)            │ publish telemetry.live.{tenant}.{imei}
                                             ▼
                                    service-websocket (fanout per tenant)
api-vehicle (CRUD, media, commands)   worker-alert (overspeed, geofence, offline, fuel)
service-monitor (admin docker ops)    Postgres (schema per tenant) + MinIO (media)
```

## Key Features
- **Protocol Ingestion:** Supports multiple GPS protocols (GT06, Teltonika) over TCP.
- **Message Broker:** NATS JetStream ensures durable messaging with DLQ support.
- **Multi-Tenancy:** Data is isolated per company using PostgreSQL schemas.
- **Real-time:** `service-websocket` handles low-latency position updates to the frontend.
- **Security:** JWT authentication, CORS, rate limiting, and RBAC implementations. No hardcoded credentials.

## Build and Run

To build any service, navigate to its directory and run:
```bash
go build ./...
```
*Note: Due to the workspace configuration, run builds per-service rather than from the repository root.*

For local development using Docker Compose:
```bash
docker-compose -f docker-compose.local.yml up -d
```

## Environment Variables
Copy `.env.example` to `.env` and fill in the required credentials for DB, Redis, NATS, and MinIO. **Never commit `.env`, `.env.local`, or `.env.coolify` to the repository.**

## Recent Security Audit Updates
- Removed hardcoded credentials and debug endpoints.
- Cleaned up git history from large binary files and `.bak` backups.
- Corrected build failures in `service-websocket`.
- Secrets tracking disabled via `.gitignore`.
