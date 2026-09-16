# ADATRACK Backend

Backend **Real-Time GPS Tracking & Fleet Management Platform** — multi-tenant (B2B) dengan arsitektur event-driven: ingest frame perangkat GPS melalui TCP, dorong pembaruan live via WebSocket, simpan telemetry ke PostgreSQL (schema-per-tenant), lengkap dengan auth JWT, RBAC row-level, dan audit trail.

> **Single source of truth** untuk kebutuhan produk: [`PRD.md`](PRD.md). README ini hanya panduan developer cepat.

## Status Roadmap

| Fase | Lingkup | Status |
|---|---|---|
| **B0** | Fondasi: compose infra, migrasi, `internal/` shared pkg | ✅ Selesai |
| **B1** | Pipeline data: `ingestion-tcp` · `worker-live` · `worker-persistence` | ✅ Selesai |
| **B2** | `service-websocket`: REST + WebSocket + RBAC + auto-provision tenant | ✅ Selesai |
| **B3** | `worker-alert` + `api-vehicle`: alarm engine (GEOFENCE/OVERSPEEDING/SOS/BATTERY/OFFLINE/ROUTE_DEVIATION + notifikasi) & fleet CRUD (vehicles/geofences/routes/assignments/speed-configs + soft delete/restore + RBAC row-level) | ✅ Selesai |
| B4–B12 | Fuel, dashcam, fleet core, hardening, protokol tambahan, dll. | ⬜ Planned |

Verifikasi B2 (2026-09-15): `make e2e-ws` **21/21 PASS** (push WS end-to-end 4 ms), provisioning FR-5.5/FR-5.6 **31/31 PASS**, `make test -race` bersih.
Verifikasi B3 (2026-09-16): `scripts/test.sh` **exit 0 semua modul** (unit test geometri Haversine/ray-casting, konfigurasi speed & grace band, RBAC row-level, lifecycle acknowledge/resolve, validasi geometri + pagination); migrasi company `008–012` idempoten & ledger-audited. E2E live per-alert mengikuti setelah infra compose tersedia.

## Arsitektur

```
Perangkat GPS (GT06 / Teltonika, port 200+ protokol referensi Traccar)
        │  TCP :9003 (GT06) · :9011 (Teltonika)
        ▼
┌─────────────────┐   telemetry.raw.>    ┌──────────────┐  telemetry.live.<IMEI>  ┌───────────────────┐
│  ingestion-tcp  │ ───────────────────▶ │ worker-live  │ ──────────────────────▶ │ service-websocket │
│  parse protokol │                      │ state live   │                         │  REST + WebSocket │
└─────────────────┘                      └──────┬───────┘                         └─────────┬─────────┘
        │               NATS JetStream (retensi 48 h / 4 GiB)               HTTP :8082 / WS /ws/v1/adatrack
        │                      ▲                      │                                   ▲
        │                      │ telemetry.persist.>  │  batch insert                     │ Frontend
        ▼                      │                      ▼                                   │
     Redis ◀───────────────────┴──────────── worker-persistence ──▶ PostgreSQL ◀──┘
   (live state,                        (batch → th_telemetry_logs)   (master + schema per-tenant)
    auth denylist,
    rate limit)
```

Alur: frame perangkat → NATS → **worker-live** (update state Redis + publish `telemetry.live.<IMEI>`) → konsumsi oleh **service-websocket** (push `VEHICLE_UPDATE` ke klien WS, target < 1 s) dan **worker-persistence** (batch insert ke `th_telemetry_logs`).

Alur B3 (alert & fleet): stream `telemetry.raw.>` juga dikonsumsi **worker-alert** — evaluasi geofence (circle/polygon), overspeeding (grace band), SOS, battery low, offline, route deviation → insert `th_alerts` + publish `alert.sos.<IMEI>` / `notify.alert.<vehicle_id>` sesuai `tm_notification_preferences`. **api-vehicle** melayani manajemen armada (`/api/v1/vehicles|geofences|routes|speed-configs|alerts` + soft delete/restore) dengan JWT & denylist Redis yang sama dengan service-websocket dan RBAC row-level yang konsisten.

## Tech Stack

| Layer | Teknologi |
|---|---|
| Bahasa | Go 1.25 (Go modules per service/tool) |
| Ingestion TCP | `net` stdlib — parser GT06 + Teltonika (dapat diperluas) |
| Message broker | NATS 2.10 + JetStream |
| Live state / cache | Redis 7 (`go-redis/v9`) |
| Database | PostgreSQL 15 (`pgx/v5`, schema-per-tenant) |
| REST API | Gin (`service-websocket`, `api-vehicle`) |
| WebSocket | Gorilla WebSocket |
| Auth | JWT HS256 (`golang-jwt`) + bcrypt cost 12 (`golang.org/x/crypto`) |
| Metrics | Prometheus client (`/metrics` per service) |
| Media (rencana) | MinIO / S3-compatible |

## Struktur Repo

```
├── PRD.md                     # Dokumen master kebutuhan (single source of truth)
├── .agent/                    # Aturan agent + roadmap & checklist fase (B0–B12)
├── internal/                  # Shared pkg: config, pg/redis/nats client, tenant, audit, logging
├── services/
│   ├── ingestion-tcp/         # TCP server + parser protokol perangkat (GT06, Teltonika)
│   ├── worker-live/           # Konsumsi telemetry → Redis live state + publish live
│   ├── worker-persistence/    # Konsumsi telemetry → batch insert PostgreSQL
│   ├── worker-alert/          # Alarm engine: geofence/overspeed/SOS/battery/offline/route → th_alerts + notifikasi
│   ├── api-vehicle/           # REST fleet management: vehicles/geofences/routes/speed-configs/alerts
│   ├── service-websocket/     # REST /api/v1 + WS /ws/v1/adatrack + auth/RBAC/audit
│   └── foundation-check/      # Pemeriksaan kesiapan infra (self-check)
├── tools/
│   ├── e2e/                   # Harness E2E pipeline (frame → NATS → Redis+PG)
│   └── e2ews/                 # Harness E2E REST + WebSocket (login → RBAC → live push)
├── database/
│   ├── init-pg/               # Bootstrap DB/role saat container postgres init
│   └── migrations/
│       ├── master_pg/         # Migrasi schema master (001–019, ledger tm_schema_migrations)
│       └── company_pg/        # Migrasi schema per-tenant (dijalankan saat provisioning)
├── deployments/
│   └── docker-compose.coolify.yml   # Varian produksi (Coolify)
├── docker-compose.yml         # Infra LOCAL (postgres, redis, nats, minio)
├── scripts/                   # compose-up, migrate, reset-db, e2e-*, start-services, test
├── docs/                      # Arsitektur DB, HA, runbook, panduan perangkat, acuan frontend
└── Makefile                   # Entry point developer (make help)
```

## Prasyarat

- **Go ≥ 1.25**, **Docker + Docker Compose**, **GNU Make**
- (Opsional, untuk E2E di host) `psql`, `redis-cli`, `curl`

## Quickstart (LOCAL)

```bash
# 1. Konfigurasi — salin template, sesuaikan nilai
cp .env.example .env.local

# 2. Infrastruktur (postgres, redis, nats, minio)
make up                    # VARIANT=local (default)

# 3. Bootstrap DB + migrasi master + seed
make migrate

# 4. Build semua modul Go → bin/
make build

# 5. Jalankan pipeline services di host (log di logs/)
make services-up

# 6. Cek kesehatan
curl -s localhost:8082/healthz
```

> Docker daemon tidak tersedia di mesin dev? Services dapat dijalankan langsung di host (`make services-up`) selama PostgreSQL/Redis/NATS dapat dijangkau — semua host/koneksi diatur lewat `.env.local`.

## Konfigurasi

Dua varian konfigurasi terpisah (PRD §7 — tidak boleh tercampur):

| Varian | File | Kegunaan |
|---|---|---|
| `LOCAL` | `.env.local` | Dev/staging; dipilih `make up` default |
| `COOLIFY` | `.env.coolify` | Produksi (Coolify); secret dari resource Coolify |

Template lengkap + penjelasan tiap variabel: [`.env.example`](.env.example). Yang penting:

- **`JWT_SECRET` wajib** (≥ 32 karakter) — service menolak boot tanpa kunci eksplisit.
- `MIGRATE_ON_BOOT=true` — migrasi dijalankan otomatis saat boot service (ledger `tm_schema_migrations`, advisory lock).
- `TELEMETRY_INTERVAL_SECONDS=20`, `WS_MAX_CONNECTIONS=5000`, `BATCH_SIZE=500`, dst.

## Services & Port

| Service | Port | Fungsi |
|---|---|---|
| `ingestion-tcp` | TCP `9003` (GT06), `9011` (Teltonika), metrics `:8090` | Terima + parse frame perangkat |
| `worker-live` | metrics `:8091` | Live state Redis + publish `telemetry.live.<IMEI>` |
| `worker-persistence` | metrics `:8092` | Batch insert `th_telemetry_logs` |
| `service-websocket` | HTTP `:8082` (`/healthz`, `/metrics`) | REST + WebSocket + auth |
| PostgreSQL | `5432` (host `5533`) | Master + schema per-tenant |
| Redis | `6379` (host `6380`) | Live state, auth, rate limit |
| NATS | `4222` (monitor `8222`) | JetStream bus |

## Make Targets

```text
make help             # daftar lengkap
make up / down / ps / logs    # infra compose (VARIANT=local|coolify)
make migrate          # bootstrap + migrasi + seed + verifikasi ledger
make reset-db         # DROP schema & provision ulang (DESTRUKTIF!)
make provision-tenant CODE=ACME NAME="PT Acme"   # provisioning tenant via CLI
make build            # build semua modul → bin/
make test             # unit + integration test semua modul
make test-race        # test dengan race detector
make fmt / vet        # gofmt / go vet semua modul
make services-up / services-down   # jalankan/stop services di host
make e2e              # E2E pipeline (frame → NATS → Redis + PG)
make e2e-ws           # E2E REST + WS (login → RBAC → live push)
```

## API & WebSocket (ringkas)

Semua response memakai envelope PRD §8.1 (`{status, data, pagination}` / `{status, error_code, message, timestamp, errors}`).

| Method | Path | Akses | Fungsi |
|---|---|---|---|
| POST | `/api/v1/auth/login` | publik | Login (bcrypt, rate limit 5×/15 menit) |
| POST | `/api/v1/auth/refresh` | publik | Rotasi refresh token (opaque, one-time) |
| POST | `/api/v1/auth/logout` | auth | Revoke token (denylist `jti` + refresh) |
| POST | `/api/v1/companies` | platform | **Auto-provision tenant** (FR-5.5): schema + migrasi + admin |
| POST | `/api/v1/users` | platform | Buat user tenant (FR-5.6) |
| GET | `/api/v1/vehicles` | tenant | Daftar kendaraan (RBAC row-level, filter, pagination) |
| GET | `/api/v1/vehicles/:id` | tenant | Detail + enrich live state (posisi, speed, fuel, …) |
| GET | `/api/v1/vehicles/:id/history` | tenant | Riwayat posisi (`from`/`to`, maks 90 hari) |
| GET | `/ws/v1/adatrack?token=<JWT>` | auth | WebSocket live tracking |

Contoh subscribe WebSocket:

```json
{"action": "subscribe", "vehicle_ids": [1, 2]}
```

Push per perangkat (event `VEHICLE_UPDATE`, payload FR-5.2): posisi, speed, ACC, satellites, altitude, gsm_signal, battery, fuel, `plate_number`.

## Multi-Tenancy

- Satu database fisik, banyak schema: `adatrack_gps_master` + `adatrack_gps_{code}` per perusahaan.
- Provisioning satu panggilan (`POST /api/v1/companies`): buat schema + jalankan seluruh migrasi tenant + seed role-menu + akun admin `admin@{code}.local` (`must_change_password=true`) + audit trail.
- Isolasi akses: RBAC row-level per perusahaan + `tm_user_vehicles` (Operator/Driver hanya kendaraan yang di-assign); tenant selalu ditentukan dari **token**, bukan body request.

## Testing

```bash
make test              # semua modul
make test-race         # dengan race detector
make e2e               # E2E pipeline data (butuh services + infra hidup)
make e2e-ws            # E2E REST + WebSocket (butuh service-websocket hidup)
```

- Unit/integrasi `service-websocket` mencakup auth, RBAC matrix, REST contract, WS flow (handshake, subscribe RBAC, reconnect, drop-oldest), audit redaksi, dan adapter Redis nyata (miniredis).
- Harness E2E mensimulasikan perangkat GT06 sungguhan (frame 1:1 → ingestion → NATS → worker-live → WS klien).

## Observability

- Setiap service mengekspos `/healthz` + `/metrics` (Prometheus).
- NATS monitor: `http://localhost:8222`.
- Audit trail (`tm_audit_logs`, append-only + trigger) untuk aksi sensitif; 401/403 tercatat sebagai `ACCESS_DENIED`.

## Dokumentasi Lanjutan

- [`PRD.md`](PRD.md) — kebutuhan lengkap (arsitektur §4, skema §6, konfigurasi §7, API §8, keamanan §9, deployment §14, roadmap §20).
- [`docs/`](docs/) — arsitektur DB, HA/DR, runbook insiden, panduan koneksi perangkat, acuan struktur frontend.
- [`.agent/03-backend-phases.md`](.agent/03-backend-phases.md) — checklist & bukti verifikasi per fase backend.


