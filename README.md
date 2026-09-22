# ADATRACK Backend

Backend **Real-Time GPS Tracking & Fleet Management Platform** — multi-tenant (B2B),
arsitektur event-driven: frame perangkat GPS masuk lewat TCP, diproses paralel untuk
live tracking (WebSocket) dan penyimpanan telemetry (PostgreSQL schema-per-tenant).

> Kebutuhan produk lengkap ada di [`PRD.md`](PRD.md) (SSOT). README ini hanya
> menjelaskan teknologi, arsitektur, isi repo, dan cara menjalankannya.

## 1. Techstack

| Lapisan | Teknologi |
|---|---|
| **Bahasa** | Go **1.25** — satu Go module per service (`services/*`), kode bersama di modul `internal/` (via `replace adatrack_gps/internal => ../../internal`) |
| **HTTP / REST** | Gin `v1.12` |
| **WebSocket** | gorilla/websocket `v1.5` |
| **Auth** | JWT HS256 (`golang-jwt/jwt v5`) + refresh token opaque one-time + revocation denylist `jti` di Redis; password **bcrypt cost 12** (`golang.org/x/crypto`) |
| **Database** | PostgreSQL **15** — satu database, banyak schema: `adatrack_gps_master` + `adatrack_gps_{company_code}`; driver `jackc/pgx v5` (pgxpool); migrasi versioned + ledger `tm_schema_migrations` (advisory lock) |
| **Cache / state** | Redis **7** — state live kendaraan, denylist token, rate limit (login & API) |
| **Message broker** | NATS **2.10** + **JetStream** — 6 stream: `telemetry-raw`, `telemetry-live`, `telemetry-error`, `alert`, `notify`, `media`; retensi **48 jam / 4 GiB** per stream |
| **Object storage** | S3-compatible — **MinIO** (LOCAL & Coolify untuk sementara). Semua akses lewat antarmuka S3 sehingga bisa ditukar ke S3/R2/GCS hanya dengan mengganti `MEDIA_S3_*` |
| **Observability** | Prometheus (setiap service expose `/metrics`), Alertmanager, Grafana (dashboard `adatrack-core` terprovision), node-exporter, cAdvisor, postgres-exporter, redis-exporter |
| **Protokol perangkat** | GT06 & Teltonika (parser internal, mengacu keluarga protokol Traccar) |
| **Packaging** | Dockerfile multi-stage per service (build `golang:1.25-alpine` → runtime `alpine` non-root, healthcheck via `/healthz`) |
| **Orkestrasi** | Docker Compose v2 (satu stack: infra + monitoring) dan **Coolify** untuk production — deploy satu klik dengan migrasi otomatis saat boot |

## 2. Arsitektur

```
   Perangkat GPS — GT06 · Teltonika (keluarga protokol Traccar; TK103 menyusul)
                     │ TCP :9003 (GT06) · :9011 (Teltonika)
                     ▼
            ┌────────────────┐   parse + normalisasi frame
            │  ingestion-tcp │   (auth IMEI, koreksi koordinat, kanal fuel 0x0D)
            └───────┬────────┘
                    ▼ publish
   ┌────────────────────────── NATS JetStream ──────────────────────────┐
   │ telemetry-raw · telemetry-live · telemetry-error · alert · notify  │
   │ · media          (retensi 48 jam / 4 GiB per stream)               │
   └──┬──────────────────────┬────────────────────────┬─────────────────┘
      │ telemetry.raw.>      │ telemetry.raw.>        │ telemetry.live.>
      ▼                      ▼                        ▼
 ┌─────────────┐     ┌────────────────────┐   ┌────────────────────┐
 │ worker-live │     │ worker-persistence │   │ service-websocket  │
 │ state live  │     │ batch insert       │   │ push VEHICLE_UPDATE│
 │ ke Redis    │     │ (batch/timeout)    │   │ ke klien WS        │
 └──────┬──────┘     └─────────┬──────────┘   └─────────┬──────────┘
        │                      │                        │
        │  telemetry.live.<IMEI>                      HTTP :8082 · WS /ws/v1/adatrack
        └──────────────┐       │                        │
                       ▼       ▼                        ▼
   ┌──────────────┐  ┌──────────────────┐        ┌──────────────┐
   │ worker-alert │  │   PostgreSQL     │◀──────▶│  api-vehicle │
   │ detektor +   │─▶│ master + schema  │        │ REST fleet   │ HTTP :8081
   │ notifikasi   │  │ per tenant       │        │ mgmt         │
   └──────────────┘  └──────────────────┘        └──────────────┘
            │ alert.> · notify.alert.<vehicle_id>
            └──────────────────────────────────────────────▶ (konsumen: audit,
                                                             notifikasi eksternal)
```

**Alur ringkas**

1. `ingestion-tcp` menerima frame TCP, memvalidasi & menormalkannya, lalu mem-publish ke `telemetry.raw.<IMEI>`.
2. `worker-live` menghitung state live (posisi, speed, ACC, fuel, …) dan menyimpannya di Redis, lalu mem-publish `telemetry.live.<IMEI>`.
3. `service-websocket` mengonsumsi `telemetry.live.>` dan mem-push event `VEHICLE_UPDATE` ke klien yang berhak (target < 1 s).
4. `worker-persistence` mengonsumsi `telemetry.raw.>` dan menulis batch ke `th_telemetry_logs` (partisi bulanan).
5. `worker-alert` mengevaluasi aturan alarm (geofence, overspeeding, SOS, battery low, offline, route deviation, fuel drop) → menulis `th_alerts` + publish `alert.>` dan `notify.alert.<vehicle_id>`, dengan rate limit + audit.
6. `api-vehicle` melayani CRUD fleet (kendaraan, geofence, route + assignment, speed/fuel config, alert acknowledge/resolve) dan overlay state live dari Redis.
7. `service-websocket` juga melayani auth (login/refresh/logout), provisioning tenant, dan daftar/detail/riwayat kendaraan.

**Multi-tenancy**

- Satu database fisik, banyak schema: `adatrack_gps_master` + `adatrack_gps_{company_code}` per perusahaan.
- Tenant **selalu** ditentukan dari token, bukan body/query request.
- Provisioning satu panggilan (`POST /api/v1/companies`): buat schema + apply seluruh migrasi tenant + seed role-menu + akun admin `admin@{code}.local` (`must_change_password=true`) + audit trail.
- Isolasi akses: RBAC per perusahaan + `tm_user_vehicles` (Operator/Driver hanya kendaraan yang di-assign).

**Tabel utama** — `tm_*` master, `th_*` transaksi/riwayat, `td_*` detail. Yang bervolume tinggi (`th_telemetry_logs`, `th_fuel_logs`, `th_media_events`) memakai partisi bulanan + soft delete global (`deleted_at`) untuk tabel master.

## 3. Struktur Repo

```
backend/
├── PRD.md                        # SSOT kebutuhan produk
├── Makefile                      # entry point developer (make help)
├── docker-compose.yml            # stack kanonik: postgres, redis, nats + monitoring
├── docker-compose.local.yml      # varian LOCAL (bind 127.0.0.1 + MinIO) — include file di atas
├── .env.example                  # template konfigurasi → copy ke .env.local
│
├── internal/                     # shared module: config, NATS/JetStream, Redis, dialect PG,
│                                 # tenant manager, metrics, storage (S3), load.go
├── services/                     # SATU Go module + Dockerfile per service
│   ├── ingestion-tcp/            # TCP device ingress (GT06/Teltonika)
│   ├── worker-live/              # state live → Redis
│   ├── worker-persistence/       # batch insert → PostgreSQL
│   ├── worker-alert/             # engine alarm + notifikasi (WS/email/SMS) + audit
│   ├── service-websocket/        # REST auth/tenant + WebSocket realtime
│   ├── api-vehicle/              # REST fleet management (CRUD + alert lifecycle)
│   └── foundation-check/         # CLI diagnosa kesiapan stack
│
├── database/
│   ├── init-pg/                  # bootstrap schema + seed referensi (idempoten)
│   ├── migrations/master_pg/     # migrasi master (versioned, ledger tm_schema_migrations)
│   └── migrations/company_pg/    # migrasi schema tenant
│
├── deployments/
│   ├── docker-compose.coolify.yml  # varian Coolify (deploy 1 klik) → docs/DEPLOY_COOLIFY.md
│   ├── docker-compose.local.yml    # entry point tipis (include ../docker-compose.local.yml)
│   └── nats/nats.conf              # konfigurasi JetStream (limit store, max_age/max_bytes)
│
├── monitoring/
│   ├── prometheus/               # scrape config (LOCAL + varian coolify) + rules alert/SLO
│   ├── grafana/                  # provisioning datasource + dashboard `adatrack-core`
│   ├── alertmanager/             # routing notifikasi alert
│   └── targets/                  # file_sd hasil generate (scripts/gen-prom-targets.sh)
│
├── scripts/                      # compose-up, migrate, reset-db, start-services, test,
│                                 # backup/restore/retensi, gen-prom-targets, b4-verify
├── tools/                        # e2e (pipeline) · e2ews (REST+WS) · querybench (SLA query)
├── docs/                         # arsitektur DB, HA/DR, runbook, panduan perangkat,
│                                 # acuan frontend, deploy Coolify
└── bin/ · logs/ · backups/       # artefak runtime (gitignored)
```

## 4. Prasyarat

| Kebutuhan | Versi / catatan |
|---|---|
| **Go** | 1.25 atau lebih baru (semua `go.mod` memakai `go 1.25.0`) |
| **Docker Engine** | dengan `docker compose` **v2.24.4+** — stack memakai `include:` dan tag `!override` pada `ports` |
| **GNU Make** | untuk seluruh target di `Makefile` |
| **Klien PostgreSQL (`psql`)** | dibutuhkan `make migrate`, `backup-db`/`restore-db`, dan `scripts/b4-verify.sh` (skrip konek ke `127.0.0.1:$HOST_PG_PORT`) |
| **`python3` + `curl`** | dipakai skrip verifikasi & cek monitoring (file_sd, JSON dashboard, `/healthz`) |
| **Sumber daya** | ± 8 GB RAM untuk 11 container (infra + monitoring) sekaligus 6 service host-run |
| **Port bebas di host** | `5533` `6380` `4222` `8222` `9000` `9001` `9003` `9011` `8081` `8082` `8090–8092` `9093` `9095` `9100` `8084` `9187` `9121` `3001` |
| **OS** | Linux, macOS, atau Windows + Docker Desktop (WSL2) |

Divalidasi pada Go 1.26 · Docker Compose v5.3.1 · GNU Make 4.4.1 · Python 3.14 · psql 18.

## 5. Quickstart

```bash
# 1. Siapkan konfigurasi (nilai dev sudah aman sebagai default)
cp .env.example .env.local

# 2. Nyalakan stack: PostgreSQL + Redis + NATS + MinIO + monitoring (11 container)
make up

# 3. Bootstrap + migrasi + seed referensi + verifikasi ledger
make migrate

# 4. Build & jalankan 6 service pipeline di host (log ada di logs/*.log)
make services-up

# 5. Cek kesehatan (semua harus {"status":"ok"})
curl -s localhost:8090/healthz   # ingestion-tcp  (postgres + nats + tenant pools)
curl -s localhost:8091/healthz   # worker-live    (nats + redis)
curl -s localhost:8092/healthz   # worker-persistence
curl -s localhost:8094/healthz   # worker-alert
curl -s localhost:8081/healthz   # api-vehicle
curl -s localhost:8082/healthz   # service-websocket
```

Monitoring (opsional, `make up` sudah menyalakannya):

```bash
make prom-targets            # tulis file_sd target service host-run
# Grafana     http://localhost:3001  (admin / admin — dashboard uid adatrack-core)
# Prometheus  http://localhost:9095
# Alertmanager http://localhost:9093
```

Uji end-to-end:

```bash
make e2e                   # frame perangkat → NATS → Redis + PostgreSQL
make e2e-ws                # login → RBAC → live push WebSocket
make test / test-race      # unit + integration semua modul
```

| Target | Fungsi |
|---|---|
| `make help` | daftar lengkap target |
| `make up` / `down` / `ps` / `logs` | stack compose (VARIANT=local\|coolify) |
| `make migrate` | bootstrap + migrasi + seed + verifikasi ledger |
| `make services-up` / `services-down` | jalankan/stop service di host |
| `make reset-db` | **DESTRUKTIF** — drop schema ADATRACK & provision ulang |
| `make provision-tenant CODE=ACME NAME="PT Acme"` | provisioning tenant via CLI |
| `make build` / `fmt` / `vet` | build / format / analisis semua modul |
| `make querybench CODE=DEV001` | bench SLA query |
| `make b4-verify` | rantai acceptance B4 (`QUICK=1` untuk smoke cepat) |
| `make backup-db` / `restore-db` / `retention-purge` | backup, drill restore, retensi partisi |

**Production (Coolify):** lihat [`docs/DEPLOY_COOLIFY.md`](docs/DEPLOY_COOLIFY.md) —
paste environment lalu satu klik Deploy; migrasi dijalankan otomatis saat service boot.

## 6. Service & Ports

### Service aplikasi (dijalankan `make services-up`, atau container di Coolify)

| Service | Peran | Listen | Healthz / Metrics | Env pengatur |
|---|---|---|---|---|
| `ingestion-tcp` | Ingress TCP perangkat GPS; parse GT06/Teltonika → `telemetry.raw.>` | **TCP 9003** (GT06) · **9011** (Teltonika) | `:8090` | `TCP_PORT`, `TELTONIKA_TCP_PORT`, `INGESTION_METRICS_ADDR` |
| `worker-live` | Hitung state live → Redis, publish `telemetry.live.<IMEI>` | — (consumer) | `:8091` | `LIVE_METRICS_ADDR`, `LIVE_BATCH_INTERVAL_MS` |
| `worker-persistence` | Batch insert telemetry → PostgreSQL | — (consumer) | `:8092` | `PERSISTENCE_METRICS_ADDR`, `BATCH_SIZE`, `BATCH_TIMEOUT_SEC` |
| `worker-alert` | Detektor alarm + notifikasi + audit | — (consumer) | `:8094` | `ALERT_METRICS_ADDR`, `OFFLINE_AFTER_MINUTES`, `SMTP_*`, `SMS_*` |
| `api-vehicle` | REST fleet management (CRUD + alert lifecycle) | **HTTP 8081** | `:8081` | `API_VEHICLE_HTTP_ADDR` |
| `service-websocket` | REST auth/provisioning + WebSocket realtime | **HTTP/WS 8082** | `:8082` | `HTTP_ADDR` |

Semua service mengekspos `/healthz` (readiness) dan `/metrics` (Prometheus). Untuk `api-vehicle` dan `service-websocket` keduanya berada di listener yang sama dengan port HTTP-nya; empat service pipeline (`ingestion-tcp` + 3 worker) memakai port metrics terpisah seperti tabel di atas.

### Infra & monitoring (container, `make up`) — port host di varian LOCAL

| Komponen | Port host (LOCAL) | Port internal | Env |
|---|---|---|---|
| PostgreSQL 15 | `5533` | `5432` | `HOST_PG_PORT` |
| Redis 7 | `6380` | `6379` | `HOST_REDIS_PORT` |
| NATS 2.10 | `4222` · monitor `8222` | `4222` · `8222` | `HOST_NATS_PORT`, `HOST_NATS_MONITOR_PORT` |
| MinIO | `9000` (API) · `9001` (console) | `9000` · `9001` | `HOST_MINIO_PORT`, `HOST_MINIO_CONSOLE_PORT` |
| Prometheus | `9095` | `9090` | `HOST_PROM_PORT` |
| Alertmanager | `9093` | `9093` | `HOST_ALERTMANAGER_PORT` |
| Grafana | `3001` | `3000` | `HOST_GRAFANA_PORT` |
| node-exporter | `9100` | `9100` | `HOST_NODE_EXPORTER_PORT` |
| cAdvisor | `8084` | `8080` | `HOST_CADVISOR_PORT` |
| postgres-exporter | `9187` | `9187` | `HOST_PG_EXPORTER_PORT` |
| redis-exporter | `9121` | `9121` | `HOST_REDIS_EXPORTER_PORT` |

Port host di atas **hanya untuk varian LOCAL** dan seluruhnya di-bind ke `127.0.0.1` (tidak terekspos ke LAN).

### Varian Coolify (production)

- Hanya **port perangkat GPS** yang di-publish ke host (`TCP_PORT`, `TELTONIKA_TCP_PORT` — default `.env.coolify` `5001` & `5027`) karena device connect TCP langsung.
- Service HTTP (`service-websocket` 8082, `api-vehicle` 8081, Grafana 3000) **internal-only**; akses publik lewat proxy + TLS Coolify (isi kolom Domains dengan port internal tersebut).
- Infra & monitoring tidak punya port host sama sekali.

## 7. API & WebSocket

### Envelope response

Sukses:

```json
{"status": "success", "data": { }, "pagination": {"page": 1, "limit": 100, "total": 42}}
```

Error:

```json
{"status": "error", "error_code": "VALIDATION_ERROR", "message": "...", "timestamp": "2026-09-22T07:00:00Z", "errors": [{"field": "imei", "message": "required"}]}
```

### Autentikasi

- `POST /api/v1/auth/login` → access token **JWT HS256** (`JWT_EXPIRY_HOURS`, default 24 jam) + refresh token opaque **one-time** (`JWT_REFRESH_EXPIRY_HOURS`, default 168 jam).
- Refresh **wajib rotasi**; logout menaruh `jti` ke denylist Redis ber-TTL (`JWT_REVOCATION_ENABLED=true`).
- Rate limit: login `LOGIN_RATE_LIMIT` (default 5× / 15 menit) + lockout `LOGIN_LOCKOUT_THRESHOLD`; API umum `API_RATE_LIMIT` (default 100 / menit) per user.
- Kirim token sebagai header `Authorization: Bearer <jwt>`. Tenant diambil dari token, **bukan** dari body/query.

### service-websocket — `:8082`

| Method | Path | Akses | Fungsi |
|---|---|---|---|
| POST | `/api/v1/auth/login` | publik | Login (bcrypt + rate limit) |
| POST | `/api/v1/auth/refresh` | publik | Rotasi refresh token (one-time) |
| POST | `/api/v1/auth/logout` | auth | Revoke access + refresh token |
| POST | `/api/v1/companies` | platform (SuperAdmin) | **Provisioning tenant**: schema + migrasi + seed role-menu + admin tenant + audit |
| POST | `/api/v1/users` | platform (SuperAdmin) | Buat user tenant dalam satu panggilan (+ opsional assign kendaraan) |
| GET | `/api/v1/vehicles` | tenant | Daftar kendaraan (filter, pagination, `include_deleted`) |
| GET | `/api/v1/vehicles/:id` | tenant | Detail kendaraan + overlay state live (posisi, speed, ACC, fuel, …) |
| GET | `/api/v1/vehicles/:id/history` | tenant | Riwayat posisi (`from`/`to`, maks `HISTORY_MAX_RANGE_DAYS` = 90 hari) |
| GET | `/ws/v1/adatrack` | auth | **WebSocket** live tracking (lihat bagian berikut) |
| GET | `/healthz`, `/livez`, `/metrics` | — | Kesehatan, liveness, metrik Prometheus |

### api-vehicle — `:8081`

Semua endpoint butuh JWT dan berlaku RBAC row-level (kecuali disebut lain).

| Resource | Endpoint |
|---|---|
| **Kendaraan** | `GET/POST /api/v1/vehicles` · `GET/PATCH /api/v1/vehicles/:id` · `DELETE /api/v1/vehicles/:id` (Admin) · `POST /api/v1/vehicles/:id/restore` (Admin) · `GET /api/v1/vehicles/:id/fuel/history` |
| **Geofence** | `GET/POST /api/v1/geofences` · `GET/PATCH /api/v1/geofences/:id` · `DELETE /api/v1/geofences/:id` (Admin) · `POST /api/v1/geofences/:id/restore` (Admin) |
| **Route** | `GET/POST /api/v1/routes` · `GET/PATCH /api/v1/routes/:id` · `DELETE /api/v1/routes/:id` (Admin) · `POST /api/v1/routes/:id/restore` (Admin) |
| **Route assignment** | `GET/POST /api/v1/routes/:id/assignments` · `PATCH/DELETE /api/v1/routes/:id/assignments/:assignmentId` |
| **Speed config** | `GET/POST /api/v1/speed-configs` · `GET/PATCH /api/v1/speed-configs/:id` · `DELETE /api/v1/speed-configs/:id` (Admin) · `POST /api/v1/speed-configs/:id/restore` (Admin) |
| **Fuel config** | `GET/POST /api/v1/fuel-configs` · `GET/PATCH /api/v1/fuel-configs/:id` · `DELETE /api/v1/fuel-configs/:id` (Admin) · `POST /api/v1/fuel-configs/:id/restore` (Admin) |
| **Alert lifecycle** | `GET /api/v1/alerts` (filter `type`/`severity`/`status`/`from`/`to`) · `POST /api/v1/alerts/:id/acknowledge` · `POST /api/v1/alerts/:id/resolve` |
| **Ops** | `GET /healthz`, `/livez`, `/metrics` |

Pagination: `?page=&limit=` (`API_DEFAULT_PAGE_SIZE` 100, `API_MAX_PAGE_SIZE` 1000). Tabel master memakai **soft delete**, sehingga `?include_deleted=true` + endpoint `restore` tersedia.

### WebSocket — live tracking

```
GET /ws/v1/adatrack
  Authorization: Bearer <jwt>        ← atau ?token=<jwt>
```

Klien harus memakai token user yang **sudah** mengganti password (akun tenant dibuat dengan `must_change_password=true`). Pesan dari klien:

```json
{"action": "subscribe",   "vehicle_ids": [1, 2]}
{"action": "unsubscribe", "vehicle_ids": [2]}
{"action": "ping"}
```

Event dari server (`models.Event*`):

| Event | Kapan dikirim |
|---|---|
| `SUBSCRIBED` / `UNSUBSCRIBED` | Konfirmasi langganan + daftar `vehicle_ids` yang **diizinkan** (langganan di luar hak akses dibuang, bukan error) |
| `VEHICLE_UPDATE` | Fan-out dari `telemetry.live.<IMEI>`: posisi, speed, ACC, satellites, altitude, gsm_signal, battery, fuel, `plate_number` |
| `HEARTBEAT` | Balasan `action: ping` (keepalive) |
| `ERROR` | Pesan tidak valid / melampaui batas |

Proteksi yang berlaku: `WS_MAX_CONNECTIONS` (5000), `WS_MAX_SUBSCRIPTIONS` per koneksi (5000), buffer kirim `WS_SEND_BUFFER_BYTES` (256 KB), antrean `WS_MAX_QUEUE` (1000 — pesan tertua dibuang saat penuh), ukuran pesan masuk `WS_MAX_MESSAGE_BYTES` (4 KB), ping `WS_PING_INTERVAL_SEC` (30 s). Origin dibatasi `WS_ALLOWED_ORIGINS`.

### RBAC

| Peran | Lingkup |
|---|---|
| `SuperAdmin` | Platform: provisioning tenant, pembuatan user tenant lintas perusahaan |
| `Admin` | Perusahaan: termasuk operasi destruktif (delete/restore) di `api-vehicle` |
| `Manager` | Perusahaan: baca + tulis (create/update) |
| `Operator` / `Driver` | Hanya kendaraan yang di-assign di `tm_user_vehicles` (row-level) |

Akses menu per role disimpan di tabel `tm_role_menu_access` (schema tenant, di-seed saat provisioning) dan menjadi sumber kebenaran navigasi frontend.

### Subjek NATS (integrasi antar-service)

| Subject | Publisher → Consumer | Isi |
|---|---|---|
| `telemetry.raw.<IMEI>` | `ingestion-tcp` → `worker-live`, `worker-persistence`, `worker-alert` | frame ter-normalisasi |
| `telemetry.live.<IMEI>` | `worker-live` → `service-websocket` | state live kendaraan |
| `telemetry.error.>` | service mana pun | frame gagal parse (dead-letter) |
| `alert.>` | `worker-alert` | event alarm (`th_alerts`) |
| `notify.alert.<vehicle_id>` | `worker-alert` → kanal notifikasi | notifikasi per kendaraan |
| `media.>` | (B5b) `service-media` | event media dashcam |
