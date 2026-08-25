# Adatrack — Backend

Backend **Real-Time GPS Tracking & adatrack Management** untuk platform pelacakan kendaraan
**5.000+ device**, telemetri setiap **5 detik**, dengan target latensi end-to-end **< 800 ms**,
throughput **2.000 msg/s peak**, dan uptime **>= 99,9%**.

---

## Daftar Isi

1. [Arsitektur & Alur Data](#arsitektur--alur-data)
2. [Teknologi](#teknologi)
3. [Struktur Direktori](#struktur-direktori)
4. [Model Multi-Tenant](#model-multi-tenant)
5. [Layanan (Services)](#layanan-services)
6. [Persistence Provider (MySQL / PostgreSQL)](#persistence-provider-mysql--postgresql)
7. [Konfigurasi Environment](#konfigurasi-environment)
8. [Menjalankan (Setup Lokal)](#menjalankan-setup-lokal)
9. [Migrations & Seed Data](#migrations--seed-data)
10. [Konvensi NATS Subjects](#konvensi-nats-subjects)
11. [Testing & Load Test](#testing--load-test)
12. [Monitoring, SLO & Alerting](#monitoring-slo--alerting)
13. [Backup, Restore & High Availability](#backup-restore--high-availability)
14. [Tools Pendukung](#tools-pendukung)
15. [Status Fase](#status-fase)

---

## Arsitektur & Alur Data

```
                ┌─────────────────────────────────────────────────────┐
   Device GPS  ► │  ingestion-tcp  (TCP :9000 GT06 / :9001 Teltonika)  │
   GT06/Teltonika│  parse protokol → validasi IMEI → JSON standar       │
                └──────────────┬──────────────────────────────────────┘
                               │ publish telemetry.raw.<IMEI>
                               ▼
                        ┌──────────────┐      (JetStream)
                        │     NATS     │◄─── subscriptions (queue groups)
                        └──────┬───────┘
          ┌─────────┬──────────┼──────────┬─────────────┐
          ▼         ▼          ▼          ▼             ▼
   worker-live  worker-   worker-   service-       worker-alert
   (queue live) persis.   alert     websocket      (queue alert)
                ─────────           ─────────      deteksi geofence/
      │         │         │         │     │       overspeed/battery/
      ▼         ▼         ▼         ▼     │       offline/SOS/rute
   Redis    MySQL/   company DB   Bridge NATS→WS→  tabel alerts +
   live-state Postgres  alerts    VEHICLE_UPDATE   notifikasi
```

**Alur ringkas:**

1. Device terhubung via TCP ke `ingestion-tcp` (parse GT06/Concox wajib; Teltonika
   Codec 8/8E dan TK-103 sebagai listener tambahan). Hanya IMEI terdaftar
   (allowlist di `master.vehicle_imei_map`) yang diproses — **anti-spoofing**.
2. JSON telemetri yang sudah distandarkan dipublish ke NATS `telemetry.raw.<IMEI>`
   dengan dilengkapi `company_code` + `vehicle_id`.
3. Empat **queue group** mengonsumsi subjek tersebut secara independen:
   - `worker-live` → menulis live-state ke **Redis** (`{prefix}{company}:vehicle:state:<IMEI>`, TTL 5 menit).
   - `worker-persistence` → **batch insert** ke MySQL/PostgreSQL `telemetry_logs` (500 baris / 5 detik).
   - `worker-alert` → deteksi **alert & geofence** dan publish ke `alert.*`.
   - `service-websocket` → fan-out **VEHICLE_UPDATE** ke klien WebSocket ber-RBAC.
4. Frontend/dashboard memakai REST + WebSocket dari `service-websocket`
   (manajemen vehicle/geofence/routes/alerts) dan `api-vehicle` (CRUD vehicle,
   speed-config, route-assignment).

> Latency end-to-end diukur dari device → WebSocket; history di-load via query
> ter-**PARTISI** pada `telemetry_logs` (indeks `(imei, timestamp DESC)`).

---

## Teknologi

| Layer | Teknologi |
|---|---|
| Ingestion (TCP + parse protokol) | Golang |
| Message Broker / Queue | NATS / JetStream (`-js`) |
| Live State / Cache | Redis 7 |
| Persistent DB | **PostgreSQL 16** (default) *atau* **MySQL 8.0** (InnoDB, ter-PARTISI) |
| Real-Time Streaming | Golang WebSocket (gorilla/websocket) |
| REST API | Gin (`service-websocket`, `api-vehicle`) |
| Monitoring | Prometheus + Grafana + Alertmanager, mysqld/postgres-exporter, node_exporter, cAdvisor |
| Object storage (B5b) | MinIO / S3-compatible |

Semua layanan Go memakai **struktur module Go monorepo** dengan shared packages di
`backend/internal/` (direferensikan lewat `replace`). Logging structured JSON via `slog`,
metric Prometheus via `client_golang`.

## Struktur Direktori

```
backend/
├── .env / .env.example          # SATU sumber config untuk SEMUA service & infra
├── docker-compose.yml           # Infra dev (MySQL/Postgres + Redis + NATS) — provider-fleksibel
├── services/
│   ├── api-vehicle/             # REST API manajemen vehicle (Gin, JWT, RBAC)
│   ├── ingestion-tcp/           # TCP listener + parse protokol → NATS
│   ├── service-websocket/       # REST dashboard + WebSocket + RBAC + bridge NATS
│   ├── worker-alert/            # Deteksi alert & geofence → tabel alerts + notifikasi
│   ├── worker-live/             # Live-state → Redis (batch)
│   └── worker-persistence/      # Batch insert telemetry → MySQL/Postgres
├── internal/
│   ├── config.go envfile.go     # Shared config + env loader
│   ├── mysqlclient.go redclient.go natsclient.go   # Client ter-pool
│   ├── tenant/                  # Multi-tenant: master + pool company DB + read-replica
│   ├── dialect/                 # Cabang PG/MySQL (pgxdriver?, placeholder, search_path)
│   ├── tokenauth/               # JWT refresh/rotasi/revokasi (B4)
│   ├── audit.go logger.go metrics.go …   # Audit log, structured log, matrix
├── database/
│   ├── migrations/             # master/, company/, legacy_single_db/ + *_pg/ (paritas PG)
│   ├── init/ init-pg/           # bootstrap container DB (multi-tenant init)
│   ├── seed/                   # master_seed.sql, company_seed.sql, reference/ (wilayah)
│   ├── data/raw/                # sumber data generator wilayah + provenance
│   └── tools/                  # genhash (hash password), genregions (data wilayah)
├── cmd/                          # Utilitas operasional (lihat §14)
├── loadtest/                     # Simulator device GT06 (load test end-to-end)
├── monitoring/                  # prometheus.yml, alerting/slo-rules, grafana
├── deployments/
│   ├── docker-compose.yml        # entry point (include dev compose)
│   ├── docker-compose.monitoring.yml  # Prom/grafana/node_exporter/cAdvisor/…
│   └── docker-compose.ha.yml     # overlay HA: replika PG/MySQL/Redis
└── scripts/                     # compose-up, backup/restore, loadtest-suite, replication, …
```

---

## Model Multi-Tenant

Arsitektur **master DB (auth & governance) + company DB (data)**

### Master DB — `adatrack_gps_master`

`countries/provinces/cities/districts/subdistricts` (data wilayah), `companies`,
`users` (email + bcrypt, role termasuk `SuperAdmin`), `user_company_access`,
`vehicle_imei_map` (mapping device → company/vehicle, dipakai anti-spoofing),
`vehicle_categories`, `vehicle_types`, `audit_logs`, `platform_tenant` (registry).

### Company DB — `adatrack_gps_{company_code}`

Satu schema per tenant, berisi: `user_vehicles`, `vehicles`, `telemetry_logs`
(di-PARTISI), `geofences`, `geofence_vehicles`, `speed_configs`, `alerts`,
`notification_preferences`, `notifications`, `routes`, `route_assignments`.

- Autentikasi master pada login → JWT berisi `company_code` → setiap request
  di-rute ke schema `adatrack_gps_{company_code}` (atau schema khusus PG).
- **RBAC**: `user_company_access.role_override` (efektif) → `user_vehicles`
  (Admin melihat semua; role lain hanya vehicle miliknya). Cross-tenant → `403`.
- **Platform & governance**: konteks `'default'` dengan role `SuperAdmin` untuk
  endpoint platform (`POST /companies`, `POST /users`, auto-provision tenant).

---

## Layanan (Services)

| Service | Module | Port (env) | Peran |
|---|---|---|---|
| **ingestion-tcp** | `Adatrack/ingestion-tcp` | TCP `:9000` · metric `:8090` | Parse GT06 (Teltonika/TK-103 tambahan), anti-spoofing, publish `telemetry.raw` |
| **worker-live** | `Adatrack/worker-live` | metric `:8091` | Batched state ke Redis |
| **worker-persistence** | `Adatrack/worker-persistence` | metric `:8092` | Batch insert `telemetry_logs`; retry + backoff; tanpa data loss |
| **worker-alert** | `Adatrack/worker-alert` | metric `:8093` | Geofence, overspeed, battery, offline, SOS (+eskalasi/TTA), route deviation; notifikasi |
| **service-websocket** | `Adatrack/service-websocket` | HTTP `:8080` · metric `:9090` | REST dashboard + WS + RBAC + bridge NATS → `VEHICLE_UPDATE`/`notify` |
| **api-vehicle** | `Adatrack/api-vehicle` | HTTP `:8081` | REST CRUD vehicle, speed-configs, route/assignment, RBAC |
| **service-media** (B5b) | `Adatrack/service-media` | HTTP `:8095` | Ingest dashcam event media → MinIO/S3, katalog ber-RBAC |

> Semua service expose `/healthz` (readiness) dan `/metrics` (Prometheus).

## Persistence Provider (MySQL / PostgreSQL)

engine persistent dipilih via `DATABASE_PROVIDER`.

- `DATABASE_PROVIDER=postgres` (**default**) vs `DATABASE_PROVIDER=mysql`.
- docker-compose hanya men-start DB terpilih (compose profiles), memakai helper
  `backend/scripts/compose-up.sh`.
- Jalur schema terpisah: `database/init` + `database/migrations` (MySQL) dan
  `database/init-pg` + `database/migrations/*_pg` (Postgres); seed wilayah sama
  untuk dua-duanya (paritas).
- **Paritas PostgreSQL**: saat provider `postgres`, placeholder `?` ditranspile
  ke `$N` (driver `pgx5`) dan `search_path` di-terapkan per-tenant via DSN
  (`internal/dialect/pgxdriver.go`, `internal/tenant`). Lihat
  `docs/POSTGRES_PROVIDER.md`.
- Mengganti provider **tidak memigrasi data antar engine** — provisikan schema
  tujuan kemudian jalankan aplikasi.

---

## Konfigurasi Environment

Satu file `backend/.env` menjadi **single source of truth** — semua service memakainya
(via `env_file` pada compose); tidak ada `.env` per-service. Salin dari `.env.example`:

```bash
cp backend/.env.example backend/.env
```

Poin penting di `backend/.env`:

| Group | Var kunci |
|---|---|
| Provider | `DATABASE_PROVIDER`, `COMPOSE_PROFILES` |
| NATS | `NATS_URL`, `NATS_SUBJECT_PREFIX` (default `telemetry`) |
| MySQL | `MYSQL_HOST/PORT/USER/PASSWORD`, `MYSQL_MAX_OPEN_CONNS`, `MYSQL_POOL_MIN/MAX` |
| PostgreSQL | `POSTGRES_HOST/PORT/USER/PASSWORD`, `POSTGRES_POOL_MIN/MAX`, `DATABASE_URL` |
| Multi-tenant | `MASTER_DB_*`, `COMPANY_DB_PREFIX=adatrack_gps_`, `REDIS_KEY_PREFIX=adatrack_gps:` |
| Read-replica | `DB_REPLICA_ENABLED`, `DB_REPLICA_*`, `POSTGRES_REPLICA_*` / `MYSQL_REPLICA_PORT` |
| Redis | `REDIS_ADDR`, `REDIS_KEY_PREFIX` |
| JWT | `JWT_SECRET`, `JWT_EXPIRY_HOURS`, `JWT_REFRESH_EXPIRY_HOURS`, `JWT_REVOCATION_ENABLED` |
| Alert | `SPEED_LIMIT`, `SPEED_GRACE_MARGIN`, `SOS_ESCALATION_MINUTES`, `SOS_ESCALATION_MAX` |
| Rate limit | `RATE_LIMIT_LOGIN_ATTEMPTS/WINDOW`, `RATE_LIMIT_API_PER_MIN` |
| WebSocket | `WS_READ_BUFFER`, `WS_MAX_CONNECTIONS`, `WS_MAX_QUEUE`, `WS_HEARTBEAT_SECONDS`, `WS_PONG_WAIT`, `WS_WRITE_WAIT`, `WS_MAX_MESSAGE_SIZE` |
| TCP | `TCP_PORT`, `TCP_MAX_CONNECTIONS`, `TELTONIKA_TCP_PORT`, `TK103_TCP_PORT`, `GT06_DATE_BCD` |
| Bind | `INGESTION_METRICS_ADDR`, `PERSISTENCE_METRICS_ADDR`, `LIVE_METRICS_ADDR`, `ALERT_METRICS_ADDR`, `WEBSOCKET_HTTP_ADDR`, `WEBSOCKET_METRICS_ADDR`, `API_VEHICLE_HTTP_ADDR` |
| TLS | `TLS_ENABLED`, `TLS_CERT_FILE`, `TLS_KEY_FILE` (default off di dev) |
| Logging | `LOG_LEVEL` (`debug/info/warn/error`) |

> `JWT_SECRET` wajib **sama** antara `service-websocket` & `api-vehicle` agar token dapat saling
> dipakai. Jangan commit `.env` berisi secret; gunakan `.env.example` sebagai template.

---

## Menjalankan (Setup Lokal)

### 1. Bootstrap infrastruktur (PostgreSQL = default)

```bash
cd backend
cp .env.example .env        # lalu isi secret (lihat .env.example)

# Build + start infra sesuai provider di .env
scripts/compose-up.sh up -d
# verifikasi
scripts/compose-up.sh ps
```

Saat pertama volume dibuat, init script otomatis di-apply
(`database/init` untuk MySQL / `database/init-pg` untuk PostgreSQL), termasuk
master DB, company default/`dev001`, dan seed wilayah.

### 2. Build & jalankan service (host dev)

```bash
cd backend/services/ingestion-tcp      && go build -o ingestion-tcp .      && ./ingestion-tcp &
cd backend/services/worker-live        && go build -o worker-live .        && ./worker-live &
cd backend/services/worker-persistence && go build -o worker-persistence . && ./worker-persistence &
cd backend/services/worker-alert       && go build -o worker-alert .       && ./worker-alert &
cd backend/services/service-websocket  && go build -o service-websocket .  && ./service-websocket &
cd backend/services/api-vehicle        && go build -o api-vehicle .        && ./api-vehicle &
```

Ganti provider ke MySQL hanya dengan set `DATABASE_PROVIDER=mysql` lalu jalankan
ulang `compose-up.sh down/up` (perlu re-provisi schema via `database/init`).

### 3. Verifikasi cepat

```bash
curl http://127.0.0.1:8080/healthz   # service-websocket
curl http://127.0.0.1:8081/healthz   # api-vehicle
```

Kirim telemetri uji (frame GT06 valid) untuk melihat data muncul di `telemetry_logs`
& Redis (lihat `loadtest/`).

---

## Migrations & Seed Data

### Lokasi migrasi

| Jalur | Isi |
|---|---|
| `database/migrations/master/` | Master DB `001`–`012` (wilayah, companies, users, imei_map, product type, audit, platform_tenant) |
| `database/migrations/master_pg/` | Paritas master PostgreSQL |
| `database/migrations/company/` | Company DB `001`–`012` (access, vehicles, telemetry partition, geofences, speed_configs, alerts, notifications, routes, route_assignments) |
| `database/migrations/company_pg/` | Paritas company PostgreSQL |
| `database/migrations/legacy_single_db/` | Skema legacy single-DB (tidak dipakai) |

`POST /companies` (auto-provision tenant) menerapkan 12 migrasi company + seed
secara otomatis → `adatrack_gps_{company_code}` (dan schema PG setara).

### Seed / reference

- `database/seed/master_seed.sql`, `database/seed/company_seed.sql`.
- `database/seed/reference/001–005_*.sql` — data wilayah real
  (countries → provinces → cities → districts → subdistricts), idempoten,
  dihasilkan `database/tools/genregions`. Provenance & limitasi:
  `database/data/raw/README.md`.

### Tools

- `cmd/migrate-tenant` — terapkan migrasi company (ber-tenant).
- `cmd/partition-maintain` — maintenance partition `telemetry_logs` (buat `p_future`, dsb.).
- `database/tools/genhash` — generate bcrypt hash (untuk password user master).
- `database/tools/genregions` — generator SQL seed wilayah dari sumber BPS, dsb.

---

## Konvensi NATS Subjects

Subjek NATS:

- `telemetry.raw.<imei>`   : raw telemetry (queue `persistence`, `live`, `ws`, `alert`)
- `telemetry.live.<imei>`  : update live ke WebSocket
- `telemetry.error.<imei>` : parse error
- `alert.geofence.<company>` · `alert.speed.*` · `alert.offline.*` · `alert.battery.*` · `alert.sos.*`
- `alert.fuel.<company>`   : 
- `media.event.<company>` · `media.capture.request.<company>` : dashcam event media — plan B5b
- `notify.alert.<vehicle_id>` : notifikasi ke WebSocket (fan-out RBAC)
- Queue groups: `persistence`, `live`, `websocket`, `alert` (+ `media` utk service-media).

---

## Testing & Load Test

### Unit test

```bash
cd backend/services/<service> && go test ./...   # per service
cd backend/internal           && go test ./...
cd backend/loadtest           && go test ./...   # parser GT06 1:1 vs frame contoh
```

Paket murni yang punya unit test non-infra: `internal/dialect`, `internal/tenant`,
`internal/tokenauth`, worker-live/persistence controllers, worker-alert (geometri/
severity), service-websocket & api-vehicle controllers, parser ingestion.

### Simulator device (`loadtest/`)

Simulator **GT06** end-to-end yang menghasilkan frame *valid* 1:1 dengan parser
(CRC-ITU 2-byte + lat/lon integer), memakai IMEI terdaftar:

```bash
cd backend/loadtest && go build -o loadtest . && ./loadtest --help
```

Skrip load test menyeluruh (`scripts/`) **sadar-dialek** (memilih engine dari
`DATABASE_PROVIDER`) dan env-safe:

- `scripts/loadtest-suite.sh`      — baseline + peak + endurance 24 jam, verifikasi delta otomatis.
- `scripts/verify-endurance.sh`    — verifikasi hasil endurance (Δ MySQL/Postgres = SENT).
- `scripts/endurance-checkpoint.sh`— checkpoint untuk endurance tahan-crash.

Hasil validasi: E2E 1000 msg/s sustained **0 data loss**; 2000 msg/s peak 0 loss.

---

## Monitoring, SLO & Alerting

- Stack: `monitoring/prometheus.yml`, `monitoring/alerting-rules.yml`, `monitoring/slo-rules.yml` (uptime ≥ 99,9% → error budget +
  burn-rate), `monitoring/grafana/`.
- Exporter: `mysqld_exporter`/`postgres-exporter`, `node_exporter`, `cAdvisor`.
- Up via `deployments/docker-compose.monitoring.yml`.
- Setiap service Go expose `/metrics` (`client_golang` Go collector: goroutines,
  GC, resident memory) + matrix (`http_*`, `ws_*`, `rbac_*`, `tenant_*`,
  `db_*`, `alerts_created_total`, `batch_insert_*`, dsb.).
- Insiden (CPU/Memory/RDS/service stuck): ikuti `docs/INCIDENT_RUNBOOK.md`.

---

## Backup, Restore & High Availability

Bagian `scripts/`:

| Script | Peran |
|---|---|
| `backup-mysql.sh` / `restore-mysql.sh` | dump per-DB gzip + SHA256 checksum, verifikasi restore + row count |
| `backup-redis.sh` | BGSAVE snapshot best-effort Redis |
| `compose-up.sh` | up infra sesuai `DATABASE_PROVIDER` |
| `loadtest-suite.sh` / `verify-endurance.sh` / `endurance-checkpoint.sh` | baseline + peak + endurance 24 jam dengan verifikasi delta otomatis |
| `gen-dev-cert.sh` | generate sertifikat TLS dev |

Replikasi/HA (overlay `deployments/docker-compose.ha.yml`) — **model READ REPLICA**:

- `mysql-replica` (GTID, `read_only`) / `postgres-replica` (streaming WAL) / `redis-replica`.
- Replika **skala BACA** (laporan/analitik) — read/write split di aplikasi
  (`internal/tenant` ReadPool/ReadRouter: READ→replika, WRITE→primary, dengan
  circuit-breaker + fallback). Bukan mekanisme backup (tetap dump harian + uji
  restore) dan bukan failover.
- Skrip: `scripts/replication/setup-mysql-replication.sh`,
  `setup-postgres-replication.sh`, `replication-status.sh`,
  `drill-redis-failover.sh`.
- Detail lengkap & prosedur: `docs/HIGH_AVAILABILITY.md`.

---

## Utilita (`cmd/`)

| Perintah | Fungsi |
|---|---|
| `db-replica-probe` | Probe kedalaman read-replica (pakai `ReadPool`/`Primary`) |
| `migrate-tenant` | Terapkan migrasi untuk tenant (company) |
| `partition-maintain` | Pelihara partisi `telemetry_logs` |
| `querybench` | Query benchmark SLA (history 30 hari < 1.5 s, geofence < 500 ms) |
| `tools/e2e-pub` | Publisher NATS utilitas untuk skenario E2E |

---

## Status Fase

Ringkas (detail lengkap di `.clinerules/03-backend-phases.md`):

| Fase | Status |
|---|---|
| B0 — Infrastruktur & Foundations | ✅ |
| B1 — Pipeline Data (ingestion → Redis + MySQL/Postgres) | ✅ |
| B2 — service-websocket (REST + WS + RBAC) | ✅ |
| B3 — Alerts, Geofence & api-vehicle | ✅ |
| B5a — Fuel Sensor | ⬜ Not started |
| B5b — Dashcam Event Media | ⬜ Not started |
| B4 — Performance, Monitoring, Hardening, HA | 🔄 In progress |

> **Frontend (F1–F4) tidak boleh dimulai sampai seluruh fase backend (B0–B4)
> selesai** — lihat `.clinerules/02-roadmap-overview.md` & `.clinerules/04-frontend-phases.md`.

---

**Catatan:** Dokumen ini adalah panduan ringkas; untuk detail batas/hak hingga
persetujuan ide teknis, selalu acu `.env.example`, `docker-compose.yml`, dan
dokumentasi fase di atas sebagai sumber kebenaran.