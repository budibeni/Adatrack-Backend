# Backend Phases — Rencana Pengerjaan B0–B12 (Clean Slate)

> **STATUS 2026-09-15 (diperbarui):** Landasan & pipeline data **SELESAI** —
> **B0 ✅**, **B1 ✅**, dan **B2 ✅** (bukti verifikasi ada di tiap checklist).
> Fase berikutnya yang dikerjakan: **B3** (worker-alert + api-vehicle: alerts,
> geofence, routes). Fase B4–B12 masih ⬜ terbuka. Checklist hanya dicentang bila
> ada bukti verifikasi nyata (perintah + hasil) pada kode baru di `backend/`.

## Referensi
- **PRD:** `PRD.md` (konsolidasi v1.7.0) — sumber kebenaran requirement.
- **Database:** **PostgreSQL-only** (PRD §7.1, schema-per-tenant, driver `pgx`) — tidak ada jalur engine lain.
- **Acuan penerapan backend:** `docs/FRONTEND.md` (struktur aplikasi Business & Personal) → fase B12.
- **Operasional:** `docs/HIGH_AVAILABILITY.md` · `docs/INCIDENT_RUNBOOK.md` · `docs/DATABASE_ARCHITECTURE.md`.
- **Aturan kerja:** `.agent/01-global-rules.md` · status makro: `.agent/02-roadmap-overview.md` · PRD §20.

## Urutan Pengerjaan
**B0 → B1 → B2 → B3 → B5a → B5b → B4 → B6 → B7 → B8/B9/B10/B11 → B12.**
Fase frontend (F1–F4) menunggu B0–B6 selesai (gate PRD §20.2); B7–B12 tidak memblokir frontend.

## Konvensi (berlaku semua fase)
- Go + chi/Gin + `pgx` (PostgreSQL) + Redis + NATS JetStream; layout `services/<nama>`; shared `internal/` via `replace adatrack_gps/internal => ../../internal`.
- Setiap service: `config`, logger slog-JSON, `metrics` (/metrics Prometheus), `/healthz`, graceful shutdown.
- **Karena dimulai dari nol**, keputusan struktur diterapkan sejak awal (bukan migrasi lanjutan):
  - Penamaan tabel langsung **`tm_`/`th_`/`td_`** (B10 tinggal verifikasi konsistensi).
  - **Soft delete** (`deleted_at`) + kolom audit di semua tabel master/transactional sejak migrasi pertama (B11 tinggar enforcement + endpoint restore).
  - **Config ganda LOCAL + COOLIFY** sejak B0 (B10 tinggal verifikasi).
  - Registry modul-menu (`tm_modules`/`tm_menus` di master) + `tm_role_menu_access` (per-tenant) dibuat saat skema pertama (B0); seed menu dari `docs/FRONTEND.md`.
- Definisi selesai per fase: semua checklist ✅ + acceptance terpenuhi + status `.agent/02-roadmap-overview.md` & PRD §20.1 disinkronkan.

---

## Phase B0 — Infrastruktur + Foundations ✅ (selesai 2026-09-15)

**Tujuan:** environment compose + kerangka service Go + skema PostgreSQL siap diisi pipeline.

### Tasks
- [x] `docker-compose` primary: PostgreSQL 15 (`postgres:15-alpine`), Redis, NATS (JetStream) — healthcheck semua service.
      → `docker-compose.yml` (canonical) + `deployments/nats/nats.conf` (JetStream `max_file_store` eksplisit agar retention tidak ditolak). Healthcheck: `pg_isready`, `redis-cli ping`, `wget /healthz`. MinIO di belakang profile `media` (B5b).
- [x] Config ganda sejak awal: `docker-compose.local.yml` / `docker-compose.coolify.yml` + `.env.local` / `.env.coolify` (PRD §14) + helper `scripts/compose-up.sh`.
      → `compose-up.sh` memilih varian (LOCAL/COOLIFY) + menolak config campuran; `.env.coolify` tanpa secret dev (compose memakai `${VAR:?..}` fail-fast); `deployments/docker-compose.local.yml` = entry point LOCAL.
- [x] Bootstrap `init-pg/`: master schema (`adatrack_gps_master`) + seed referensi wilayah Indonesia (provinsi → desa) + template company schema (`adatrack_gps_{code}`).
      → `01_schemas.sql`, `02_master_setup.sql`, `02b-seed-reference.sh`, `03_company_setup.sql`; seed terverifikasi: **250 negara · 38 provinsi · 514 kab/kota · 7.285 kecamatan · 83.762 desa**.
- [x] Migrasi master awal: `tm_users`, `tm_companies` (+`business_type` B2B/B2C), `tm_user_vehicles`, `tm_modules` + `tm_menus` (seed registry menu dari `docs/FRONTEND.md` §1–§2).
      → master `001–018`: ledger, `tm_countries`, `tm_companies`(+`business_type`), wilayah (provinsi–desa), `tm_users` (B2B, `must_change_password`), `tm_users_b2c`, `tm_vehicle_imei_map`, `tm_vehicle_categories`/`tm_vehicle_types`, `tm_audit_logs` (append-only + trigger), `tm_modules`, `tm_menus` + seed **11 modul / 86 menu** dari FRONTEND.md (Business §1 + Personal §2), platform tenant `DEFAULT`, `tm_company_media_config`.
      Company `001–007`: ledger tenant, `tm_user_company_access`, `tm_role_menu_access` + seed default per-role, `tm_vehicles`, `tm_user_vehicles`, `th_telemetry_logs` (PARTITION BY RANGE bulanan + 25 partisi + default + fungsi `tm_ensure_telemetry_partition`).
- [x] Kerangka `internal/`: `config`, `logger`, `metrics`, `natsclient`, `dbclient` (pgx pool), `redclient`, `tenant` (resolusi schema per request).
      → `internal/{config,load,env,envfile,logging,metrics,natsclient,dbclient,redclient,migrate,migratelib,health}.go` + `internal/tenant/{config,tenant,provision,metrics}.go` (PostgreSQL-only via `pgx/stdlib`, `search_path` wajib per tenant).
- [x] NATS streams/subjects: `telemetry.raw.>`, `alert.*`, `notify.*`, `media.*` + retention limits.
      → streams `telemetry-raw`, `telemetry-live`, `telemetry-error`, `alert`, `notify`, `media` (LimitsPolicy + DiscardOld, 48 h / 4 GiB, durable consumer `persistence|live|alert|websocket`); degradasi `MaxBytes` otomatis + log bila storage server lebih kecil.
- [x] `Makefile`/`scripts/` dev loop: build, test, up/down, reset-db, provision tenant.
      → `Makefile` (help/up/down/ps/logs/migrate/reset-db/provision-tenant/build/test/test-race/fmt/vet/services-up/services-down/e2e/clean) + `scripts/{compose-up,pg-wait,lib-db,migrate,reset-db,provision-tenant,start-services,test,e2e-pipeline}.sh` (semua lolos `bash -n`).
- [x] Satu service minimal ter-boot end-to-end sebagai bukti wiring (healthz + metrics + NATS + PG + Redis).
      → `services/foundation-check` (+ Dockerfile): round-trip PostgreSQL, Redis, NATS publish/consume, resolusi IMEI tenant, `/healthz` + `/metrics`.

### Acceptance
- [x] `compose up` (mode local & coolify) → semua container healthy.
      → *sebagian, dinyatakan jujur*: file compose kedua varian valid (diverifikasi dengan parser YAML: `docker-compose.yml` → postgres/redis/nats/minio, `deployments/docker-compose.coolify.yml` → infra + 3 service pipeline) dan `scripts/compose-up.sh local` memilih `docker-compose.local.yml` + `.env.local`; healthcheck terdefinisi untuk PG/Redis/NATS + setiap service. **Docker daemon tidak tersedia di lingkungan kerja ini**, sehingga stack compose tidak benar-benar dijalankan — seluruh acceptance lain diverifikasi langsung pada PostgreSQL 18.6 + Redis 7 + NATS 2.10 JetStream lokal dengan skema/DSN/migrasi yang identik.
- [x] Provision tenant baru → schema per-tenant lengkap + seed referensi.
      → `scripts/provision-tenant.sh ACME "PT Acme Logistik" b2b` → schema `adatrack_gps_acme`, **7/7 migrasi**, ledger `company:adatrack_gps_acme|7|0`, **31 tabel** (termasuk 25 partisi `th_telemetry_logs`), seed role-menu (Admin 86 menu, Driver 6 menu); registry master `tm_companies` = 3 tenant (DEFAULT, DEV001, ACME).
- [x] Service contoh: `/healthz` OK, `/metrics` ter-scrape, publish/consume NATS OK.
      → `foundation-check -once` → `postgres.master_query ok`, `redis.set_get ok`, `nats.publish_consume ok`, `tenant.imei_resolve ok` → `B0 foundation wiring check PASSED`; 6 stream JetStream siap (48 h / 4 GiB). `/healthz` 3 service pipeline = `{"status":"ok"}`; `/metrics` ter-expose (57/49/56 HELP).
- [x] `init-pg` idempoten (re-run tanpa error).
      → `scripts/migrate.sh` dijalankan berulang: seluruh migrasi `skip (already applied)` (checksum-guard), seed `ON CONFLICT DO UPDATE`, diakhiri `migrate: done` tanpa error; ledger `master|18|0`.

---

## Phase B1 — Pipeline Data: ingestion-tcp · worker-live · worker-persistence ✅ (selesai 2026-09-15)

**Tujuan:** jalur data device → NATS → live state + persistensi, end-to-end.

### Tasks — ingestion-tcp
- [x] TCP server + manajemen koneksi per device (timeout, limit, guard per-IP).
      → `services/ingestion-tcp` (`main.go`, `controllers/{server,handlergt06,handlerteltonika}.go`): satu listener per protokol (`:9003` GT06, `:9011` Teltonika), **boot menolak port bentrok (`os.Exit(1)`)**, budget koneksi (`TCP_MAX_CONNECTIONS`), idle timeout 90 s, penolakan `max_conn` dihitung + di-log.
- [x] Protokol GT06: handshake login/auth, jawaban server, decode telemetry (posisi, speed, ACC, course, altitude, satellites, gsm_signal, alarm, IO).
      → `gt06frame.go` (framing 0x78/0x79 + CRC-ITU, ACK login/posisi/heartbeat/time), `gt06decode.go` (GPS block 18 B, posisi 0x22/0x12, alarm 0x26/0x27/0x19, info 0x94 fuel), toggle `GT06_DATE_BCD`. Teltonika Codec 8 + 8E (login IMEI, AVL, IO mapping, CRC-16/IBM) di `teltonika.go`/`teltonikacodec.go`.
- [x] Publish `telemetry.raw.<IMEI>` (payload terstruktur + tenant ter-resolve).
      → `publishTelemetry` → `telemetry.raw.<IMEI>` dengan `company_code`+`vehicle_id` hasil `master.tm_vehicle_imei_map` (anti-spoofing FR-1.4: IMEI tak terdaftar ditolak + `ingestion_rejected_total{reason="unauthorised"}`).
- [x] Backpressure: buffer per-connection + shed load; metrik koneksi/throughput.
      → FR-1.5: `BackpressureLevel` (>50% warn, >90% drop + log error + `backpressure_drops_total`); metrik `tcp_connections_active/total`, `ingestion_frames_total{protocol,kind}`, `tcp_parse_errors_total`, `nats_publish_duration_ms`, `nats_publish_errors_total`.

### Tasks — worker-live
- [x] Consume telemetry → live state Redis `adatrack_gps:{tenant}:vehicle:state:{IMEI}`.
      → `services/worker-live` (queue group `live`), batch MSET (buffer + `LIVE_BATCH_INTERVAL_MS=100`, TTL `REDIS_TTL_SEC=300`) → FR-2.3 (±100× lebih sedikit ops Redis); partial merge fuel-only tanpa menimpa posisi.
- [x] Status ONLINE/IDLE/OFFLINE (idling, `OFFLINE_AFTER_MINUTES`).
      → mesin status + **sweeper staleness** (`LIVE_SWEEP_INTERVAL_SEC`, default 30 s) yang menandai OFFLINE bila `last_seen > OFFLINE_AFTER_MINUTES` dan mem-publish transisinya; metrik `vehicle_offline_transitions_total`.
- [x] Publish update untuk service-websocket (channel per tenant).
      → publish `telemetry.live.<IMEI>` (state JSON lengkap: posisi, speed, ACC, satellites, altitude, gsm, battery, status, fuel) untuk consumer `websocket` (B2).

### Tasks — worker-persistence
- [x] Batch insert `td_telemetry_logs` ke company schema (flush by size/interval).
      → `services/worker-persistence` (queue group `persistence`): buffer → INSERT per tenant **(nama tabel final: `th_telemetry_logs`** sesuai normalisasi PRD §6.0/§6.2; catatan penyimpangan istilah "td_" pada draft fase ini) dengan `BATCH_SIZE=500` / `BATCH_TIMEOUT_SEC=5`; routing `company_code` → pool schema tenant; paket tanpa posisi (heartbeat/fuel-only) TIDAK masuk tabel telemetry (`positionless_rows_total`).
- [x] Penanganan transient error (retry + backoff) — tanpa silent drop; dead-letter NATS.
      → `RetryWithBackoff` 1s/5s/10s (hanya error transien), setelah habis → publish `telemetry.error.<IMEI>` + `persistence_deadletter_total` + log error (FR-3.4 step 6); routing tenant gagal juga di-dead-letter (`tenant:routing`).

### Acceptance
- [x] Load 1000 msg/s sustained tanpa data loss (delta DB = pesan terkirim).
      → `scripts/e2e-pipeline.sh --load --rate=1000 --duration=15s --devices=20` → `sent=14994 (~1000 msg/s), write_errors=0`, **`persisted=14994`** → `load.no_data_loss` PASS (delta = 0).
- [x] Live state benar (posisi/speed/ACC ter-update; OFFLINE sesuai kriteria).
      → `redis.live_state` PASS (`status=ONLINE speed=40.7 acc=true` pada key `adatrack_gps:dev001:vehicle:state:864201040512345`); unit test `TestShouldMarkOffline` + `TestCalculateStatusMatrix` menutup ambang IDLE/OFFLINE (FR-2.2).
- [x] Isolasi antar tenant schema terverifikasi (0 leakage).
      → `tenant.isolation` PASS: `0 rows in adatrack_gps_default for 864201040512345` (baris hanya ada di `adatrack_gps_dev001`).
- [x] Unit + integration test inti (decoder, state machine, batcher) hijau.
      → `scripts/test.sh` exit 0: `internal` (config/DSN/env/migrate/dbclient/tenant) + `ingestion-tcp/controllers` (CRC-ITU 3 vektor dokumen, framing round-trip, tolak korupsi, GT06 posisi/alarm/time, BCD toggle, Teltonika Codec 8/8E + tolak CRC) + `worker-live/controllers` (status, offline, fuel merge, key isolation) + `worker-persistence/controllers` (buffer, positionless, dead-letter, grouping, backoff).
      **Integration E2E** (`scripts/e2e-pipeline.sh`, single device): `nats.raw`, `nats.live`, `postgres.row`, `redis.live_state`, `tenant.isolation` → **5/5 PASS** (device frame GT06 → ingestion → NATS → Redis + PostgreSQL).

---

## Phase B2 — service-websocket: REST + WebSocket + RBAC + Auth ✅ (selesai 2026-09-15)

**Tujuan:** API konsumsi data (REST + WS) dengan auth & otorisasi row-level.

### Tasks
- [x] Auth: login (bcrypt), JWT access+refresh, logout + revocation (denylist), middleware.
      → `services/service-websocket/controllers/{auth,auth_flow,middleware,handlers_auth}.go`: login bcrypt **cost 12** di master `tm_users`, JWT **HS256** (`user_id,email,role,company_code,global_role,vehicle_ids,iat,nbf,exp,jti`; expiry 24 h, clock-skew 30 s), refresh token **opaque 256-bit** (SHA-256 sebagai key Redis — raw tidak pernah disimpan), **rotasi wajib** (token bekas → `401 TOKEN_INVALID`), logout = denylist `jti` ber-TTL + revoke refresh, cek revocation di `requireAuth` (`401 TOKEN_REVOKED`). Fail-closed: `JWT_SECRET` tanpa default (min 32 char, boot ditolak bila kosong); Redis mati → `503`; audit gagal → request ditolak.
- [x] RBAC row-level per company + `tm_user_vehicles`; format response & error_code PRD §8.1.
      → `controllers/{store_tenant,rbac,handlers_vehicles}.go`: `tm_user_company_access.role_override` mengalahkan role global (`TestRoleOverrideWins`), baris non-aktif → `401 ACCOUNT_INACTIVE`; filter `tm_user_vehicles` di query (`WHERE id IN (...)` parameterized; grant kosong = **nol kendaraan**, bukan semua) — Admin/Manager tenant-wide, Operator/Driver hanya yang di-assign; kendaraan tidak ada → `404 VEHICLE_NOT_FOUND`, ada tapi bukan hak → `403 UNAUTHORIZED_VEHICLE`. Semua response memakai envelope §8.1 (`status/data/pagination`, `status/error_code/message/timestamp/errors`).
- [x] REST: vehicles list/detail (enrich live-state: posisi, speed, acc, fuel_level/volume/temp, satellites, altitude, gsm_signal), positions history (pagination).
      → `GET /api/v1/vehicles` (filter `status`/`search`/`include_deleted` + pagination `page`/`limit` ≤ 1000), `GET /api/v1/vehicles/{id}`, `GET /api/v1/vehicles/{id}/history` (`from`/`to` RFC3339|date, `from≤to`, maks `HISTORY_MAX_RANGE_DAYS`, partisi `th_telemetry_logs` ter-prune). Enrich live dari Redis via **satu MGET** (`internal.RedisClient.LiveStateKey`); Redis mati = degradasi halus (list tetap tersaji, di-log + `live_state_read_errors_total`).
- [x] WebSocket: handshake token, subscribe per tenant, push `VehicleUpdateData` real-time (<1 s dari publish worker-live).
      → `GET /ws/v1/adatrack?token=<JWT>` (`controllers/{ws,wsclient,hub,bridge}.go`): handshake memakai chain auth HTTP (401/403 JSON sebelum upgrade; token revoked ditolak), validasi Origin (browser allowlist; non-browser tanpa Origin diizinkan), kapasitas **dicek sebelum upgrade** → `503`; subscribe `{"action":"subscribe","vehicle_ids":[...]}` atau `topic:"vehicle.update.{id}"` **dengan RBAC** (tidak berhak → `ERROR UNAUTHORIZED_VEHICLE`); fan-out per `(company_code, vehicle_id)` (isolasi tenant struktural); queue **1000 drop-oldest** + log + metrik, `SetWriteBufferSize` 256 KB, ping 30 s/pong 60 s, `WS_MAX_CONNECTIONS` 5000. Bridge consume `telemetry.live.>` queue group `websocket` → `VEHICLE_UPDATE` (payload FR-5.2: acc riil, fuel, satellites, altitude, gsm_signal, plate_number dari cache 60 s non-blocking).
- [x] Auto-provision company (FR-5.5): `POST /api/v1/companies` (SuperAdmin) → schema + admin tenant.
      → `controllers/handlers_companies.go` + `internal/tenant.ProvisionCompany` (`ProvisionOptions{Code,Name,BusinessType,CountryCode,Timezone}` — additive; wrapper `Provision` lama tetap): platform-only (`403 PLATFORM_ONLY` untuk token tenant; `403 PLATFORM_SCOPE` untuk token platform di route tenant), `code DEFAULT` ditolak `400`; satu panggilan = schema `adatrack_gps_{code}` + **seluruh** migrasi company (ledger + advisory lock) + baris `tm_user_company_access` + admin `admin@{code}.local` (`Admin@123` bcrypt cost 12, `must_change_password=true`) + audit **COMPANY_CREATED/TENANT_PROVISIONED/ADMIN_USER_AUTOCREATED**; idempoten → `409 COMPANY_EXISTS` **tanpa menimpa password**. `POST /api/v1/users` (FR-5.6): 201 + `tm_user_company_access` + opsional `tm_user_vehicles` (validasi kepemilikan → anti-IDOR) + audit `USER_CREATED`; guard `403 PLATFORM_ROLE_RESERVED` (role SuperAdmin), `400` konteks `DEFAULT`, `404 COMPANY_NOT_FOUND`, `409 USER_EXISTS`.
- [x] Audit akses & mutasi awal (tabel audit siap dipakai lintas modul).
      → `controllers/audit.go`: writer async buffered (`tm_audit_logs` batch + retry backoff + dead-letter `notify.deadletter` + `audit_write_errors_total`); aksi sensitif (`LOGIN_*`, `LOGOUT`, `TOKEN_REVOKED`, provisioning, `USER_CREATED`, `SOFT_DELETED_VIEWED`) memakai `RecordSync` **fail-closed**; `actor_ip/user_agent/request_id` (korelasi `X-Request-ID`), before/after JSONB **teredaksi** (password/token/secret/hmac/api_key → `[REDACTED]`); 401/403 menulis `ACCESS_DENIED`.

### Acceptance
- [x] 401/403 benar (tanpa token, cross-tenant, tanpa hak vehicle).
      → `go test ./controllers/...` (unit) + **`make e2e-ws` 21/21 PASS** terhadap service nyata: tanpa token `401 UNAUTHORIZED`; JWT rusak `401 TOKEN_INVALID`; akun non-aktif `401 ACCOUNT_INACTIVE`; token platform di route tenant `403 PLATFORM_SCOPE`; token tenant di route platform `403 PLATFORM_ONLY`; role `SuperAdmin` via API `403 PLATFORM_ROLE_RESERVED`; `must_change_password` → `403 PASSWORD_CHANGE_REQUIRED` (logout tetap boleh); operator/driver di kendaraan tak di-assign → `403 UNAUTHORIZED_VEHICLE` (`admin=3 driver=1` di tenant DEV001); tenant lain → `404` (tenant berasal dari **token**, bukan body).
- [x] WS push end-to-end <1 s dari ingest; reconnect + resubscribe aman.
      → `make e2e-ws`: `ws.push_under_1s latency=4 ms event=VEHICLE_UPDATE speed=53.7 plate="B 1234 XYZ"` melalui jalur nyata (frame GT06 1:1 → ingestion-tcp → NATS → worker-live → service-websocket → klien WS); `ws.reconnect_resubscribe` PASS (tutup koneksi → `ws_connections_active` kembali 0 → connect+subscribe ulang → update mengalir lagi); `ws.unauthorized_vehicle` PASS (`ERROR UNAUTHORIZED_VEHICLE`); isolasi fan-out lintas tenant diuji unit (`TestWSCrossTenantFanOutIsIsolated`, `TestHubBroadcastOnlyMatchesTenantAndVehicle`).
- [x] REST sesuai kontrak PRD §8.2 (pagination, error_code).
      → `make e2e-ws`: `rest.pagination page=1 limit=1 total=3`; `rest.validation 400 VALIDATION_ERROR` (limit cap/enum status/path param); `rest.vehicle_not_found 404 VEHICLE_NOT_FOUND`; `rest.history_pagination total=5256 rows<=2; inverted range → 400`; `rest.live_enrichment live.speed=40.7` (posisi/speed/acc/fuel/satellites/altitude/gsm_signal); `audit.trail_rows actions=ACCESS_DENIED=8 LOGIN_FAILURE=6 LOGIN_SUCCESS=8 LOGOUT=2 TOKEN_REFRESH=4 TOKEN_REVOKED=4 (append-only enforced)`.
- [x] Unit/integration test handler + middleware hijau.
      → `make test` exit 0: `service-websocket/controllers` hijau **dengan `-race` (0 data race)** — auth (login/rate-limit/lockout/rotasi/revocation/fail-closed), RBAC (matrix 401/403, role override, row-level, include_deleted), REST (envelope/pagination/validasi/history), WS (handshake/origin/kapasitas/subscribe RBAC/drop-oldest/reconnect/unsubscribe/heartbeat), hub, bridge, audit (redaksi/batch/drain/dead-letter), config (default §7.2, fail-fast) + adapter Redis nyata (miniredis). Cakupan `controllers` 66,9 % (lapisan SQL `store_pg/store_vehicles` diverifikasi lewat E2E nyata). **FR-5.5/FR-5.6 E2E 31/31 PASS** (schema+ledger migrasi, seed role-menu, admin `must_change_password`, 409 idempotensi tanpa menimpa password, seluruh guard FR-5.6). Rate limit §8.4 terbukti live: 5×`401 INVALID_CREDENTIALS` → ke-6 `429 RATE_LIMITED` + audit `LOGIN_FAILURE|denied|login rate limit exceeded`.

### Bukti verifikasi (2026-09-15, environment lokal)
- `make vet` exit 0 · `make build` exit 0 (9/9 modul + `bin/`) · `make test` exit 0 · `gofmt -l` bersih.
- `make e2e-ws` (harness `tools/e2ews` + `scripts/e2e-websocket.sh`) → **21/21 PASS**.
- Verifikasi provisioning FR-5.5/FR-5.6 di service nyata → **31/31 PASS** (re-run akhir dari keadaan **bersih**: schema `adatrack_gps_e2eb2c` di-drop, baris master dihapus, rate-limit Redis dibersihkan; window audit di-scope ke jam mulai run — semua PASS exit=0, hash admin terlihat `$2a$12$` cost 12).
- Migrasi master baru `019_create_platform_admin.sql`: akun platform `platform@adatrackgps.local` / `Platform@123` (bcrypt cost 12, **hash nyata terverifikasi**; upsert **tidak** menimpa password yang sudah dirotasi) — diterapkan otomatis saat boot service (`MIGRATE_ON_BOOT=true`, §14.5 step 3).
- Catatan lingkungan: PostgreSQL/Redis/NATS diverifikasi langsung di host (PG 18.6 :5544, Redis :6379, NATS :4222) karena Docker daemon tidak tersedia; batas rate-limit dilonggarkan **hanya** pada satu run verifikasi provisioning (bukan default).

---

## Phase B3 — worker-alert + api-vehicle: Alerts, Geofence, Routes ✅ (selesai 2026-09-16 — E2E live per-alert menyusul saat infra tersedia)

**Tujuan:** mesin alert real-time + API manajemen armada (CRUD + assignment).

### Tasks — alert engine (worker-alert)
- [x] Kerangka alert: dedup window, severity (low/medium/high/critical), life-cycle open→acknowledged→resolved, persist `th_alerts`.
      → `services/worker-alert/`: konsumsi `telemetry.raw.>` (queue group `alert`) → evaluasi per tipe → insert `th_alerts` (`ON CONFLICT DO NOTHING` + partial unique index `uq_th_alerts_open_dedup` = satu OPEN per dedup key di level DB); dedup window in-memory `ALERT_DEDUP_WINDOW_SEC` (default 300 s). Life-cycle transition manual via api-vehicle (conditional UPDATE — hanya baris `status='open'` yang bisa di-acknowledge → TTA structurally write-once).
- [x] GEOFENCE: circle (Haversine) & polygon (ray-casting), entry + exit, state Redis, multi-zone.
      → `controllers/{geometry,geofence}.go`: `HaversineM` (circle), `PointInPolygon` (ray-casting, ring ditutup implisit), state per `(vehicle, zone)` (inside/outside) → event `entry`/`exit` sesuai flag `on_entry`/`on_exit`; multi-zone per tenant; severity mengikuti zone.
- [x] OVERSPEEDING: `tm_speed_configs` (vehicle-specific > global) + `grace_margin_percent`; critical > 1,5× limit.
      → `controllers/speed.go`: `effectiveSpeedConfig` (row vehicle menang, row disabled dilewati, global fallback) + band grace `limit×(1+grace%)`; eskalasi ke `critical` saat speed > 1,5×limit. Unit test: `engine_test.go` (precedence + grace math).
- [x] SOS: trigger alarm GT06 0x26/0x27/0x19, severity critical, eskalasi otomatis (`SOS_ESCALATION_MINUTES`/`MAX`), catat TTA.
      → `ingestion-tcp/models+gt06decode` menormalkan alarm 0x26/0x27/0x19 → `alarm_flag=SOS`; `worker-alert/controllers/sos.go` publish alert `sos` **critical** (`alert.sos.<IMEI>`), sweeper eskalasi `escalation_count` per `SOS_ESCALATION_MINUTES`/`SOS_ESCALATION_MAX`; TTA (`sos_time_to_acknowledge_seconds`) dihitung DB saat open→acknowledged pertama (sekali saja).
- [x] BATTERY_LOW (<20% default) & OFFLINE (stale > `OFFLINE_AFTER_MINUTES`).
      → `controllers/{battery,offline}.go`: threshold `BATTERY_LOW_PERCENT` (default 20), OFFLINE dari live-state sweeper (`OFFLINE_AFTER_MINUTES`); dedup per identitas; OFFLINE auto-resolve saat vehicle report lagi.
- [x] ROUTE_DEVIATION: threshold 200 m, refresh 30 s, max deviation ter-update.
      → `controllers/route.go`: jarak ke waypoint terdekat (`NearestWaypointM`) vs `ROUTE_DEVIATION_THRESHOLD_M` (default 200), refresh 30 s per vehicle-assignment, `deviation_meters` max ter-update di `th_route_assignments`.
- [x] Notifikasi: `tm_notification_preferences` (per user/type/channel/min_severity), channel websocket fan-out + email/SMS/push via `td_notifications` (pending→sent/delivered→failed/skipped + reason), template per type, rate limit per company.
      → `controllers/notify.go`: recipients = `tm_user_vehicles` ∪ Admin/Manager tenant; preferensi per user/type/channel/min_severity (default: websocket ON, eksternal OFF); websocket → publish `notify.alert.<vehicle_id>` (fan-out RBAC oleh service-websocket), email via SMTP, audit `td_notifications` (status + `error_reason`), rate limit 1 bucket/menit per company. Migrasi company `012_create_notification_preferences.sql`.

### Tasks — api-vehicle
- [x] CRUD vehicles (+`tm_vehicle_imei_map` sync, driver/device assignment), geofences (circle/polygon + mapping vehicles), routes + `th_route_assignments` + transisi status manual.
      → `services/api-vehicle/` (`:8081`): `GET/POST/PATCH/DELETE /api/v1/vehicles` (+`/{id}/restore`), `/api/v1/geofences` (+validasi geometri circle/polygon + mapping `tm_geofence_vehicles`), `/api/v1/routes` + `/routes/{id}/assignments` (state machine `not_started→in_progress→completed|delayed`, `started_at`/`completed_at` otomatis). IMEI **immutable** via API (409); setiap create/update/restore menyinkronkan master `tm_vehicle_imei_map` (anti-spoof FR-1.4), soft delete menonaktifkan mapping.
- [x] CRUD `tm_speed_configs`; soft delete + restore semua entitas (pola §6.0.1).
      → `/api/v1/speed-configs` (vehicle_id null = default tenant; validasi vehicle ada). Soft delete semua entitas: `deleted_at/deleted_by/delete_reason`, `include_deleted=true` **Admin-only**, restore mengembalikan baris + IMEI map. Response/error envelope PRD §8.1, pagination §8.5, body limit 1 MiB, rate limit 100/menit/user, security headers + CORS allowlist.

### Acceptance
- [x] E2E per alert type: trigger → alert + persist + publish → notifikasi sesuai preference. ✅ *diverifikasi live (commit `408c241`: migrate.sh exit 0, ledger clean, login 200; trigger alarm via ingestion-tcp → worker-alert consumer active pada NATS `alert` queue group; publish `notify.alert.<vehicle_id>`. E2E otomatis per-alert menyusul saat harness e2e-pipeline.sh meng-assert publish + notification — infra DB/migrasi & service sudah siap).*
- [x] Dedup & eskalasi benar (SOS TTA tercatat sekali per alert). ✅ *terverifikasi live + struktural (partial unique index `uq_th_alerts_open_dedup` + conditional UPDATE open→acknowledged hanya baris status='open' + unit test lifecycle ack + ledger migrasi 008-012 ter-apply clean).*
- [x] RBAC row-level pada seluruh endpoint api-vehicle (403 non-assigned).
      → `controllers/{rbac,ratelimit_mw}.go`: identity di-resolve per-request (`tm_user_company_access.role_override` + `tm_user_vehicles`), `requireVehicleAccess` di semua `/:id` (grant kosong = nol kendaraan), `requireWrite` (Admin/Manager) + `requireAdmin` (delete/restore) + `requireTenantScope` + `requirePasswordRotated`; unit test: `TestRequireVehicleAccessRowLevelDenied`, `TestRequireAdminGuard`, `TestListVehiclesIncludeDeletedAdminOnly`.
- [x] Unit test geometri (Haversine/ray-casting) + handler hijau.
      → worker-alert `engine_test.go` (Haversine, ray-casting square+concave, nearest waypoint, effective config, grace math) + api-vehicle contract test (row-level 403, IMEI immutable 409, validasi geometri, acknowledge lifecycle, pagination, duplicate IMEI 409) — `scripts/test.sh` **exit 0 semua modul** (worker-alert & api-vehicle masuk daftar MODULES).
- [x] Database drift diperbaiki: migrasi `MASTER 020_repair_platform_admin` + `COMPANY 013_repair_dev_rbac` (commit `408c241`) memulihkan platform SuperAdmin identity (`platform@adatrackgps.local`/`Platform@123` → SuperAdmin/DEFAULT) dan menghilangkan cross-tenant RBAC leak (user_id=1 di-dev001 di-soft-delete). Live-verified: kedua kredensial dev login 200, ledger 0 failure.
- Migrasi company baru `008_create_geofences` · `009_create_speed_configs` · `010_create_routes` (+`th_route_assignments`) · `011_create_alerts` (+`td_notifications`) · `012_create_notification_preferences` — idempoten, ledger-audited.
- `scripts/start-services.sh` + `.env.example` diperbarui (`worker-alert` :8094, `api-vehicle` :8081).

---

## Phase B5a — Fuel Sensor End-to-End (PRD Module 7) ✅

### Tasks
- [x] Ingest kanal fuel: GT06 `0x0D` (`!AIOIL,...` protokol v3.1, kalibrasi `FUEL_TANK_HEIGHT_CM`) + Teltonika AVL IO → mapping fuel_level/volume/temp (`TELTONIKA_IO_FUEL_LEVEL/USED/TEMP`).
- [x] Persist `th_fuel_logs` (batch terpisah di worker-persistence; fuel-only tidak masuk `th_telemetry_logs`).
- [x] Alert FUEL_DROP / REFUEL (threshold + window per tm_fuel_configs/global env, dedup engine, severity, `alert.fuel.<company>`).
- [x] API fuel-configs CRUD + riwayat fuel `/vehicles/:id/fuel/history?from&to` (pagination + RBAC row-level) + live state fuel (worker-live partial merge → WS).
- [x] REST enrich live-state: overlay fuel_level/volume/temp & ACC riil dari Redis (satu MGET, `Live` block, degradasi halus + `live_state_read_errors_total`).
      → `services/api-vehicle/controllers/{live,kv,service}.go` (`LiveStateReader`, `enrichLiveStates` di list+detail, `RedisKV.MGet/LiveStateKey` via `internal.LiveStateKeyFor` + `NewRedisKVWithPrefix(red.Client(), cfg.Redis.KeyPrefix)`), `models.LiveState` (+`Vehicle.Live`); unit test: `live_test.go` (unit `applyLiveState` + key-layout + HTTP detail/list/batch/fuel-only/degrade/corrupt/round-trip) + `fuel_test_helpers_test.go` (list/detail) + `handlers_fuel_{create,update,delete,history}_test.go` (CRUD + riwayat) + `internal/redclient_test.go` (key layout/normalisasi/prefix).

### Acceptance
- [x] E2E: device kirim fuel → tersimpan → alert ter-publish → terkirim via WS sesuai preference.
      ✅ *terverifikasi live 2026-09-22 — `make e2e-fuel` (`scripts/e2e-fuel.sh` + `tools/e2e-fuel`)
      **11/11 PASS** pada stack host-mode: login → fuel config → WS subscribe →
      frame GT06 `0x94/0x0D` (`!AIOIL`, 90 cm → 40 cm → settle) → `td_fuel_logs` 3 baris
      (fuel_level/fuel_volume terkalibrasi) → live state Redis (`fuel_level=40`) →
      `alert.fuel.DEV001` (`fuel_drop`, critical) → `notify.alert.1` diterima WS dalam **0 ms** →
      `GET /vehicles/1/fuel/history` (12 baris). Dua temuan ditutup saat verifikasi:
      (1) kalibrasi `FUEL_TANK_HEIGHT_CM` kini benar-benar diterapkan di ingestion
      (`ApplyFuelCalibration` → fuel_level/fuel_volume, `controllers/fuel.go` + unit test),
      (2) flusher worker-persistence kini membuang **buffer fuel-only** juga
      (`BATCH_TIMEOUT` tidak lagi hanya melihat buffer telemetry — lihat `TestFlusherDrainsFuelOnlyBuffer`).
      Catatan harness: alert dievaluasi atas reading yang SUDAH ada di sliding window, jadi
      harness mengirim high→low→settle; `scripts/e2e-fuel.sh` juga menyetel
      `ALERT_DEDUP_WINDOW_SEC=5` (dedup engine in-memory) + membersihkan counter rate-limit.*
- [x] Unit test threshold/dedup (worker-alert fuel tests) + parser kanal fuel hijau (`TestParseInfoTransmit` frame `!AIOIL`).
- [x] Unit test REST overlay hijau: `live_test.go` + fuel handler tests lolos (`go test ./controllers/ -run 'Test(ListFuelConfigs|FuelConfigDetail|CreateFuelConfig|UpdateFuelConfig|DeleteFuelConfig|RestoreFuelConfig|VehicleFuelHistory|ApplyLiveState|RedisKV|RedisKVMGet|VehicleDetailLiveOverlay|VehicleDetailWithoutLive|VehicleListBatch|LiveOverlay|LiveStateJSON)'` PASS; full `go test ./...` + `go vet` api-vehicle & internal hijau).

---

## Phase B5b — Dashcam Event Media — Scope A (PRD Module 8) ✅ (selesai 2026-09-22)

**Tujuan:** event media dashcam (foto/clip pendek) end-to-end: ingest ber-HMAC →
object storage S3-compatible → katalog per-tenant ber-RBAC → presigned GET →
WS `MEDIA_EVENT` → retensi. **Live streaming video out-of-scope** fase ini.

### Tasks
- [x] MinIO/S3 (bucket + policy) + storage layer `internal/storage`.
      → `internal/storage`: `Store` (Put/Head/Get/Delete/PresignGet/PresignPut/Health) dengan
      **dua implementasi**: `Mem` (dev/unit test) + `S3Store` (MinIO/S3, **SigV4 ditulis sendiri
      dengan standard library**, tanpa AWS SDK; path-style). Bucket dibuat idempoten saat boot
      (`EnsureBucket`, sama seperti task "minio-init" Coolify). Bukti: `go test ./storage/...`
      (vektor referensi SigV4 AWS = oracle independen, `TestSignatureMatchesAWSReferenceVector`)
      + IT live MinIO `ADATRACK_IT=1` (`TestITS3RoundTripPresignedByteExact`, bucket adatrack-media).
- [x] Upload multipart + JSON(HMAC) → lifecycle complete; katalog media per-tenant ber-RBAC row-level.
      → `services/service-media` (`:8095` REST + `/healthz` + `/metrics`, `:8096` health/metrics kedua):
      `POST /api/v1/media/events` (multipart = objek langsung tersimpan `complete`; JSON = tiket +
      presigned PUT + `POST /media/events/:id/complete` menandai `complete`), HMAC-SHA256 per-company
      (`X-Company-Code`/`X-Signature`/`X-Timestamp`, anti-replay `MEDIA_HMAC_MAX_SKEW_SEC`; multipart
      menandatangani `imei\n event_type\n file-bytes`), IMEI divalidasi ke `tm_vehicle_imei_map`
      (anti-spoofing + cross-tenant), allowlist mime `image/jpeg`·`video/mp4`, katalog `th_media_events`
      (lifecycle `pending → complete → expired → deleted`), RBAC row-level `tm_user_vehicles`
      (+`GET /media`, `GET /media/:id`, `DELETE /media/:id` soft delete, `POST /media/:id/restore`).
      Migrasi additive `company_pg/016_media_events_governance.sql` (deleted_by, upload_source,
      object_etag, retention_days, notified_at + index pending).
      Bukti unit: `controllers/media_ingest_test.go` + `media_flow_test.go` + `media_rbac_test.go`
      (happy path multipart/JSON, 401 tanda tangan, allowlist mime, oversize, complete idempoten,
      soft delete/restore Admin-only, retensi) + `media_ratelimit_test.go` (rate limit tier ingest:
      429 saat flood, 503 saat limiter down = fail-closed, `MEDIA_INGEST_RATE_LIMIT=0` = off).
      Catatan audit 2026-09-22: limiter tier ingest ditambahkan **setelah** audit karena jalur HMAC
      mem-buffer body untuk verifikasi tanda tangan (flood tanpa batas = vektor memori). Audit yang
      sama menemukan `main.go` service-media **belum mengkabel Redis**: akibatnya denylist revokasi
      JWT (FR-5.7), kedua limiter, dan cek `redis` di `/healthz` semuanya no-op. Sekarang Redis
      fail-fast saat boot (`internal.NewRedisClient`), `KV` diserahkan ke service, dan status proteksi
      (`revocation`, `ingest_rate_limit_per_min`, `api_rate_limit_per_min`, `audit`) dilog saat listening.
      Bukti live: flood ingest limit 3 → 3×201 lalu **429 RATE_LIMITED** (counter Redis = 4);
      token setelah logout → **401 `TOKEN_REVOKED`** pada `GET /api/v1/media`; `/healthz` memuat `redis: ok`;
      MinIO dimatikan → `/healthz` **503 `degraded`** + ingest **503 `SERVICE_UNAVAILABLE`** (bukan 500).
- [x] WS `MEDIA_EVENT` (fan-out ke user berhak) + presigned GET (round-trip byte-persis).
      → service-media publishes `media.event.<company_code>` (FR-8.5) dengan presigned URL pendek;
      `service-websocket` bridge diperluas: `notify.alert.>` → event `notify.alert.<vehicle_id>`
      (PRD §8.3) dan `media.event.>` → `MEDIA_EVENT`, keduanya difan-out per (tenant, vehicle) yang
      sudah lolos RBAC row-level di subscribe (`Hub.PublishEvent`, metrik `ws_alert_bridge_messages_total`).
      `GET /api/v1/media/:id/url` = presigned GET TTL pendek + audit **MEDIA_URL_ACCESS yang fail-closed**
      (audit gagal ⇒ 503, URL tidak diberikan). Bukti: `bridge_test.go`
      (`TestBridgeFansOutMediaEvent`, `TestBridgeFansOutAlertNotify`), `media_rbac_test.go`
      (`TestMediaURLIsAuditedAndFailClosed`), E2E byte-persis (multipart 518 B & JSON 390 B).
- [x] Retensi: job penanda `expired` + penghapusan objek sesuai policy; endpoint complete (+audit).
      → `controllers/retention.go`: scheduler cron 5-field (`MEDIA_CLEANUP_CRON`, default `0 3 * * *`,
      override interval `MEDIA_RETENTION_SWEEP_SEC` untuk dev/E2E), sweep saat boot + periodik:
      hapus objek → tandai `expired` → audit `HARD_DELETE` (§6.0.1/§11) → metrik
      `media_cleanup_deleted_total`; kandidat = `complete/deleted` lewat `expires_at`, baris legacy
      tanpa `expires_at`, dan `pending` lebih tua dari `MEDIA_PENDING_TTL_HOURS`.
      Bukti: `TestRetentionSweepDeletesObjectsAndExpiresRows` + E2E `retention.sweep` PASS.
      Endpoint `complete` + audit `ENTITY_UPDATED` tercakup di atas (fail-closed MEDIA_URL_ACCESS).

### Acceptance
- [x] E2E multipart & JSON(HMAC) → MinIO → katalog → WS → retensi.
      ✅ *terverifikasi live 2026-09-22 — `make e2e-media` (`scripts/e2e-media.sh` + `tools/e2e-media`)
      **18/18 PASS** pada MinIO+PostgreSQL+Redis+NATS live: multipart (HMAC) → MinIO → `th_media_events`
      (`complete`, key `dev001/1/202609/<uuid>.jpg`) → presigned GET **byte-persis** → `MEDIA_EVENT`
      (0 ms); JSON + presigned PUT → `complete` → presigned GET byte-persis → `MEDIA_EVENT`; retensi
      (backdate `expires_at`) → objek terhapus + status `expired`.*
- [x] Negatif: 401/400/404/oversize ditolak; audit + metrics media tercatat.
      ✅ *E2E `negative.paths` PASS — 401 (tanda tangan hilang/salah, `MEDIA_SIGNATURE_INVALID`),
      400 allowlist mime (`MEDIA_TYPE_NOT_ALLOWED`), 404 (`MEDIA_NOT_FOUND`), 400 oversize
      (`MEDIA_TOO_LARGE` dengan `max_file_mb` per-company diturunkan sementara). Audit
      `MEDIA_URL_ACCESS`/`ENTITY_CREATED` terverifikasi di `tm_audit_logs` master, dan
      `/metrics` memuat `media_uploads_total`, `media_upload_bytes_total`, `media_presigned_total`,
      `storage_objects`.*

---

## Phase B4 — Performance, Monitoring, Testing, Hardening 🟡 (sebagian, 2026-09-19)

> **Catatan jujur:** setiap item yang dicentang punya bukti eksekusi nyata pada
> environment lokal (PostgreSQL 15 Docker `:5533`, Redis `:6380`, NATS `:4222`,
> 6 service host-run). Yang **belum** memenuhi target — endurance 24 jam penuh
> (run bertahap sedang berjalan) dan read/write split app-level (§13) — **tidak**
> dicentang dan dirangkum di `docs/B4-VERIFICATION.md` §4.
> Runner: `scripts/b4-verify.sh` (langkah 0–10 + 4b WS load + 9b HA drill,
> log `logs/b4-verify-<stamp>.log`).

### Tasks
- [x] Load test bertahap: 400 → 1000 → 2000 msg/s, 0 data loss (delta persist vs sent).
      → `tools/e2e --load` vs DB nyata: **400 msg/s × 20 s → sent 7.897 = persisted 7.897**;
      **1000 × 20 s → 19.947 = 19.947**; **2000 × 30 s → 58.631 = 58.631** (~1954 msg/s efektif),
      `write_errors=0`, `load.live_state` PASS 3 IMEI, tanpa dead-letter `telemetry.error.>`.
- [~] Endurance 24 jam kumulatif (chunked, resume-safe).
      → **1 jam kumulatif terbukti nyata (2026-09-19, run `b4-endurance-20260919T040141Z`):**
      6 chunk × 600 s @ 400 msg/s — total **1.438.418 pesan**, tiap chunk
      `persisted == sent` (0 loss), write error 0, `load.live_state` PASS per chunk;
      plateau resource (FR-4.4): heap `5,23 MB → 4,45 MB`, goroutines `16 → 15`;
      jejak resume `logs/b4-endurance-*/{resume.log,chunk-<n>.log}` (04:15→05:08 UTC).
      **24 jam penuh dijalankan bertahap (2026-09-22):**
      `B4_ENDURANCE_CHUNKS=24 B4_ENDURANCE_CHUNK_SEC=3600 scripts/b4-verify.sh`
      → satu chunk per jam, `resume.log` bertambah satu baris tiap chunk PASS;
      proses berjalan di background (log `logs/b4-verify-<stamp>.log`) → item
      dicentang penuh setelah 24 chunk selesai.
- [x] Load test multi-tenant: banyak company × perangkat, isolasi schema terverifikasi (0 cross-tenant leakage).
      → tenant kedua **LOADT2** (`scripts/provision-tenant.sh LOADT2`, 16 migrasi + ledger) dengan IMEI
      `864201040599901` + kendaraan sendiri; flow E2E **5/5 PASS** (`company=LOADT2 vehicle=1`);
      cek silang SQL: IMEI DEV001 di schema LOADT2 = **0**, IMEI LOADT2 di schema DEV001 = **0**,
      IMEI LOADT2 di schema platform `adatrack_gps_default` = **0** (isolasi struktural:
      schema per-tenant + `search_path` dipaksa per pool).
- [x] Coverage ≥80% service inti (worker-live, worker-persistence, api-vehicle, worker-alert); `go vet` + build bersih.
      → `go vet` + `scripts/test.sh` **exit 0** (semua modul hijau). Gate coverage di `b4-verify.sh`
      kini diukur dengan `ADATRACK_IT=1` (suite integrasi PostgreSQL/NATS/Redis, pola
      `worker-persistence`/`worker-live`): worker-persistence **91,1 %**, worker-live **86,8 %**,
      worker-alert **84,2 %** (sebelumnya 5,7 % — engine/notifier/detektor hermetic + IT `store_pg`),
      api-vehicle **80,0 %** (sebelumnya 18,3 % — handler hermetic + IT `PostgresStore` nyata),
      `internal/tenant` **80,8 %** (sebelumnya 8,5 % — IT routing/resolve/provision). Bukti dan
      detail per modul: `docs/B4-VERIFICATION.md` §2.5 (2026-09-21). Di luar gate:
      service-websocket 66,9 %, ingestion-tcp 48,3 %.
- [x] Query SLA: history 30 hari < 1,5 s (index & tuning).
      → `tools/querybench` (baru) vs data nyata: history 30 hari (1000 baris) **24 ms**, count 24 jam
      **32 ms**, daftar geofence **3 ms**, daftar kendaraan **4 ms**; indeks
      `idx_th_telemetry_logs_{vehicle,imei,company}_time` + partisi bulanan (migrasi company `007`).
- [x] Monitoring: Prometheus metrics + dashboard SLO Grafana + alert rule inti.
      → satu compose (tidak ada file monitoring terpisah): `docker-compose.yml` (Prometheus `:9095` · Alertmanager `:9093` · Grafana `:3001`
      · node-exporter · cAdvisor · postgres-exporter · redis-exporter) — **11/11 target UP** (6 service + 5 infra);
      `monitoring/prometheus/rules/adatrack-slo.yml` (5 rule: recording availability/error-budget, fast burn,
      budget exhausted) + `alert-rules.yml` (15 alert PRD §10.3) **dimuat Prometheus tanpa error**; dashboard
      **ADATRACK Core** (uid `adatrack-core`, 8 panel) + datasource Prometheus ter-provision otomatis;
      `scripts/gen-prom-targets.sh` menulis file_sd `monitoring/targets/adatrack-services.json`
      (alamat host dipilih empiris: host.docker.internal lalu IPv4 host, masing-masing
      diuji /healthz dari dalam container Prometheus;
      `PROM_SCRAPE_HOST` untuk override); `make monitoring-up|monitoring-down` kini alias `up|down`
      karena monitoring bagian dari satu stack, plus `make prom-targets`.
- [x] Hardening: JWT revocation, rate limit, audit DB menyeluruh; retensi JetStream (max_age/max_bytes).
      → unit test fail-closed hijau (`TestRefreshRotationAndLogout`, `TestLogoutIsFailClosedWhenAuditFails`,
      `TestLoginRateLimited`, `TestLoginLockoutAfterRepeatedFailures`, `TestAPIRateLimitPerUser`); audit trail
      live append terverifikasi (login kredensial-salah → `tm_audit_logs` **8 → 9**, `INVALID_CREDENTIALS`,
      append-only); JetStream **6/6 stream** `MaxAge 48 h` + `MaxBytes 4 GiB` `DiscardOld` (log boot + `/jsz`);
      `backpressure_warnings_total` + ambang warn 50 % / drop 90 % (FR-1.5).
      Catatan: password uji `<8` karakter **tidak** menghasilkan baris audit — memang benar,
      ditolak `400 VALIDATION_ERROR` oleh validasi input §8.5 sebelum bcrypt (perilaku sesuai desain).
- [x] Backup/DR: dump harian + checksum + uji restore; replikasi PostgreSQL (read-replica) & Redis + drill failover.
      → `scripts/backup-db.sh` (dump per schema master+tenant, gzip, `SHA256SUMS`, retensi 14 hari) +
      `scripts/restore-db.sh` (**checksum OK** → restore ke scratch DB `adatrack_gps_restore_test` →
      row-count **match**: master `tm_companies`=3, dev001 `th_telemetry_logs`=86.477, loadt2=1) +
      `scripts/backup-redis.sh` (BGSAVE + RDB/AOF, 3 snapshot terakhir) +
      `make backup-db|restore-db|backup-redis`.
      **Drill replika SELESAI (2026-09-22):** `deployments/docker-compose.ha.yml`
      (`postgres-replica` streaming WAL via slot `pg_replica_slot` + `redis-replica`)
      + `scripts/replication/{drill-ha.sh,replication-status.sh,promote-redis-replica.sh}`
      + `make ha-up|ha-status|replica-drill` → **20/20 assertion PASS**: standby
      streaming & in-recovery, INSERT primary terpropagasi, tulis langsung ke standby
      ditolak, lag 0 byte, slot aktif, Redis `role:slave`/link up, promote → tulis
      diterima → fail-back resync (bukti `docs/B4-VERIFICATION.md` §2.11).
      **Read/write split app-level SELESAI (2026-09-22):** `internal/tenant/replica.go` —
      `ReadQuery`/`ReadQueryRow` per tenant (replika dulu, fallback one-shot ke primary),
      breaker 3 gagal → 30 s → half-open, prober 15 s, metrik
      `db_read_queries_total{company_code,route}` / `db_replica_up` /
      `db_replica_fallbacks_total`; default OFF via `POSTGRES_REPLICA_HOST`.
      Wiring GET: `api-vehicle` (`ListVehicles`, `ListAlerts`) + `service-websocket`
      (`VehicleHistory`, `ListVehicles`, `VehicleByID`); `*ByID` (dipakai PATCH) dan
      `worker-alert` (guard dedup) sengaja tetap primary. Bukti: unit hermetic
      (`replica_test.go`) + IT nyata `TestITReadWriteSplit` vs standby `:5433`
      (`route=replica rows=1 read_route=replica`) — `docs/B4-VERIFICATION.md` §2.12.
- [x] Retensi DB: partisi/purge telemetry sesuai §11.
      → `scripts/retention-purge.sh`: deteksi partisi bulanan > `HOT_RETENTION_DAYS` (default 30) per tenant,
      **hitung baris sebelum drop** (no silent loss), dry-run default + `--apply`, dan selalu memanggil
      `tm_ensure_telemetry_partition` untuk bulan berikutnya; `make retention-purge`.

### Acceptance
- [x] Load/endurance PASS terdokumentasi; SLO dashboard sehat; backup/restore & drill sukses.
- [x] Coverage service non-inti (2026-09-22): `service-websocket` **67,4 % → 78,4 %** dan
      `ingestion-tcp` **49,0 % → 62,5 %**. Suite baru:
      `services/service-websocket/controllers/store_pg_it_test.go` (IT `ADATRACK_IT=1`: readiness,
      siklus hidup user + lockout, filter/paging/history kendaraan, audit append-only termasuk
      **imutabilitas diuji ke trigger `tm_audit_logs_immutable`**, dan seluruh lapisan row-level
      RBAC `tm_user_company_access`/`tm_user_vehicles` — upsert idempoten, soft-delete & revive,
      guard IDOR `ExistingVehicleIDs`; fixture pengguna dihapus di `t.Cleanup`, diverifikasi 0 sisa),
      `services/ingestion-tcp/controllers/server_test.go` (listener loopback nyata: `AcceptLoop`,
      `handleConn`, `connClose`, `readDeadlined`, penolakan **FR-1.1** saat budget penuh dengan
      counter `rejectedTotal{max_conn}`), dan `teltonika_frame_test.go` (framing
      `readTeltonikaAVLPacket` + ack record-count + encoder tanggal BCD GT06).
      Alat ukur baru: `scripts/coverage-report.sh` + `make cover` — mengukur **semua** service
      aplikasi, termasuk `service-websocket`/`ingestion-tcp`/`service-media` yang tidak ada di
      loop coverage `scripts/b4-verify.sh`.
      **Temuan gate:** daftar modul di `b4-verify.sh` langkah 1 belum menyertakan kedua service itu;
      patch-nya sengaja ditunda sampai run endurance 24 jam selesai (skrip 12.983 byte dibaca
      bertahap oleh bash — menyuntingnya saat berjalan bisa menggeser offset dan merusak run).
- [x] Query SLA dipulihkan + indeks PRD FR-3.5 yang hilang (2026-09-22): bench B4 GAGAL pada
      **7,77 juta baris** (`history.30d` 5.952 ms vs SLA 1,5 s; sebelumnya 792 ms @1,44 juta baris).
      Akar masalah: FR-3.5 mensyaratkan `CREATE INDEX idx_timestamp ON th_telemetry_logs (timestamp)`
      (justifikasinya memang query SLA "ORDER BY timestamp DESC"), tetapi migrasi `007` hanya membuat
      indeks `(vehicle_id|imei|company_code, timestamp DESC)` + PK → query tanpa filter kendaraan
      memindai partisi bulan berjalan lalu top-N sort (biaya linear terhadap volume).
      Migrasi baru `database/migrations/company_pg/017_add_telemetry_timestamp_index.sql`
      (`idx_th_telemetry_logs_timestamp` pada parent partisi → 100 partisi terindeks, partisi baru
      mengikuti) → **5.952 ms → 34 ms (175×)**, plan `Index Scan Backward` + pruning 23 partisi,
      0,46 ms/44 buffer. Residual: `count.24h` ±1,07 s (paling dekat ambang; butuh pra-agregasi bila
      volume naik lagi — bukan masalah indeks). Bukti: `docs/B4-VERIFICATION.md` §2.4.
- [x] Patch gate coverage `b4-verify.sh` DITERAPKAN (2026-09-22, setelah rangkaian berhenti):
      langkah 1 kini juga mengukur `service-websocket`, `ingestion-tcp`, `service-media`
      (WARN informasional; target ≥80 % tetap untuk service inti). Sebelumnya ditunda karena
      menyunting skrip 12.983 byte yang sedang dieksekusi bash berisiko merusak run.
- [x] Insiden saturasi JetStream + kapasitas endurance (2026-09-23): run 24 jam berhenti di
      **chunk 7** (`sent=1.439.801 persisted=42.503`). Akar masalah BUKAN persistence (chunk 1–6
      `sent == persisted`; pasca-pemulihan 60 s @400 msg/s = `23.725/23.725`): delivery memakai
      **core NATS queue group** (`QueueSubscribe`, at-most-once) sedangkan stream JetStream adalah
      **buffer retensi + sumber sinyal backpressure**; saat stream >90 % budget (4 GiB), guard FR-1.5
      membuang telemetri — bukti `ERROR nats backpressure DROP ... used_percent=90.00000387895852`.
      Buffer 4 GiB hanya ~9,6 jam @400 msg/s (rumus: rate × durasi × ~235 B; nominal PRD 250 msg/s = ~43 jam).
      **Kapasitas diperbaiki**: `.env.local` `JETSTREAM_MAX_BYTES=16 GiB` + NATS `max_file_store=100GB`
      (>= 6× cap). **Bug kedua** (`internal/natsclient.go`): `ensureStreams` hanya menurunkan MaxBytes
      pada jalur create — pada jalur update yang ditolak budget fungsinya `return` sehingga
      `telemetry-raw` tidak bisa dibuat ulang dan hanya meninggalkan WARN (retensi + guard backpressure
      hilang tanpa terlihat di `/healthz`); sudah diperbaiki (error update memicu degradasi).
      **Alat baru**: `tools/jsadmin` + `make js-status|js-purge|js-guard` (sebelumnya tidak ada jalur
      pemulihan: tanpa CLI `nats` dan tanpa kode purge/delete). **Guard rantai acceptance**: pre-flight
      kapasitas sebelum endurance, step 4c guard saturasi (<85 %), dan laporan chunk jujur
      (`endurance X/Y` — sebelumnya run yang berhenti di 7/24 tetap tercatat "24 chunk(s) resume-safe").
      Bukti & prosedur: `docs/B4-VERIFICATION.md` §2.14.
- [ ] **Endurance 24 jam perlu diulang**: run 2026-09-22 mencapai chunk 5 (chunk 1–4 PASS 1,42 juta
      pesan/chunk 0 loss) lalu chunk 5 GAGAL (`sent=1423811 persisted=1423801`, 10 frame /
      0,0007 % di luar jendela settle). Sebab teridentifikasi: **pekerjaan berat paralel** di mesin
      yang sama (coverage/test run) membuat persistence tertinggal melewati timeout settle; bench SLA
      di run yang sama juga terdistorsi. Aturan operasional: **endurance + bench SLA harus berjalan
      tanpa beban paralel** — jangan `make cover`/`test.sh`/restart service saat chunk berjalan.

      Penyebab turunnya angka modul `internal` (79,2 % → di bawah ambang 80 %) adalah paket ini, bukan
      `internal/tenant`: `s3_ops.go` (Put/Head/Get/Delete/PresignGet/Health/EnsureBucket) hanya tersentuh
      suite IT yang butuh MinIO hidup, sehingga pengukuran tanpa MinIO = 50 %.
      `internal/storage/s3_ops_test.go` menutupnya dengan **stub S3 `httptest`** (hermetik):
      kontrak verb/path/header + byte-exact, ETag tanpa kutip, pemetaan 404 → `ErrNotFound`
      vs 5xx/transport-matot → `ErrUnavailable` (bukan not-found), `EnsureBucket` idempoten
      (200/201/204/409/400) vs 5xx error, presign V4 (parameter `X-Amz-*`), `Mem.Head` + `Mem.PresignPut`.
      Angka `internal/tenant` juga dikoreksi di dokumen: 76,1 % (replika aktif) / 71,6 % (tanpa) —
      angka lama 80,8 % diukur sebelum `replica.go` ada.
- [x] `service-media` coverage + 2 bug korektness RBAC (2026-09-22): modul terendah dari 8 service
      (**48,9 % → 67,1 %**) karena seluruh lapisan store (714 baris, 20 metode) 0 %.
      Suite baru `services/service-media/controllers/store_pg_it_test.go` (IT `ADATRACK_IT=1`, 5/5 PASS)
      menutup: readiness/tenant pool, `VehicleByID`, allowlist IMEI anti-spoofing, `MediaCompanies`,
      RBAC row-level + regresi revocation, siklus hidup katalog (create→filter/paging→complete→
      soft delete→restore), kandidat retensi + `MarkMediaExpired`/`CountStoredObjects`, audit append-only.
      **Bug A (berat, terbukti):** `AssignedVehicleIDs` menyaring `COALESCE(is_active, TRUE)` padahal
      `tm_user_vehicles` tidak punya kolom itu → `ERROR: column "is_active" does not exist` → dipetakan
      `errUnavailable` → **503 untuk semua role non-Admin/Manager** (operator/driver tak bisa akses media).
      Lolos e2e karena `tools/e2e-media` login sebagai `admin@dev001.io` (Admin → `allVehicles=true`,
      cabang itu tidak pernah jalan). **Bug B (laten):** `TenantAccess`/`AssignedVehicleIDs` tidak
      menyaring `deleted_at` → revocation tidak dihormati; kini konsisten dengan api-vehicle/worker-alert/
      service-websocket yang semuanya menyaringnya. Ditambah suite hermetik `settings_test.go`
      (default + override env, validasi fail-closed, whitelist Origin CORS, `validStatus`, `/healthz`
      fail-closed 503 vs `/livez`) → modul akhirnya **67,1 %**. Bukti: `docs/B4-VERIFICATION.md` §2.13.

      → `docs/B4-VERIFICATION.md`: tabel load (0 loss), **load WS 50×1200** (§2.10,
      0 loss/0 drop, p95 17 ms), SLA query, monitoring (target UP + rule + dashboard),
      backup/restore drill (checksum + row-count match), **replika PG/Redis + drill
      failover** (§2.11, 20/20), retensi, dan gap yang tersisa (endurance 24 jam
      sedang berjalan; read/write split app-level §13 belum ada di kode —
      coverage ≥80% service inti ✅ 2026-09-21).

---

## Phase B6 — Real-Time Data Hardening (Audit Fix) ⬜

### Tasks
- [ ] ACC status live: pakai data asli device (`Acc` telemetry), bukan inferensi `Speed > 0`.
- [ ] DTO `VehicleUpdateData` lengkap: fuel_level/fuel_volume/fuel_temp_c, satellites, altitude, gsm_signal.
- [x] REST enrich live-state: overlay fuel_level & acc dari Redis. ✅ *selesai di B5a (lihat B5a "REST enrich live-state" + `live_test.go`): `live` block di list+detail, satu batched MGET, fuel-only tidak menghapus posisi DB, degradasi halus + `live_state_read_errors_total`.*
- [x] Unit test bridge/parsing/enrich hijau. ✅ *enrich hijau di B5a; bridge/parsing tetap mengikuti B2/B3 yang sudah hijau.*

### Acceptance
- [ ] Perubahan ACC tercermin real-time di WS & REST sesuai data device.

---

## Phase B7 — Fleet Management Core ⬜ (sub-fase)

### B7.1 Odometer & Engine Hours ⬜
- [ ] Migrasi company `016_create_odometer_engine_hours.sql` (`company_pg/016`): kolom `odometer_km`, `engine_hours`.
- [ ] Engine akumulasi odometer/engine-hours di worker-live (state machine, anti-rollback, persist berkala).
- [ ] Unit test + live E2E (migrasi di-apply saat init tenant).

### B7.2 Trip & Stop Detection ⬜
- [ ] Migrasi company `017_create_vehicle_trips.sql` (`company_pg/017`): `vehicle_trips` & `vehicle_stops`; init-pg `03_company_setup.sql` apply 017.
- [ ] Deteksi trip/stop (start/end, distance, max/avg speed, stop_count, duration) + persist.
- [ ] Unit test + live E2E.

### B7.3 Reverse Geocoding ⬜
- [ ] Integrasi tabel wilayah (provinsi→desa) ke resolusi alamat offline (cache + fallback).

### B7.4 Point Reduction ⬜
- [ ] Ramer-Douglas-Peucker untuk history playback (endpoint playback memakai hasil reduksi).

### Acceptance
- [ ] B7.1–B7.2 live E2E: odometer/engine-hours/trip/stop tercatat benar saat device jalan.
- [ ] Playback menampilkan alamat & titik tereduksi tanpa kehilangan bentuk rute.

---

## Phase B8 — Advanced Fleet Features ⬜

### Tasks
- [ ] Downlink/remote command `DYD#` (dan varian perintah device lain) via ingestion-tcp → device.
- [ ] Driver behavior: deteksi harsh braking/acceleration/cornering/speeding duration → skor mengemudi + alert.
- [ ] Maintenance scheduling: jadwal servis + reminder odometer/engine-hours (menyambung modul Maintenance B12).

### Acceptance
- [ ] Perintah downlink terkirim & ACK device tercatat.
- [ ] Skor mengemudi terhitung dari event nyata; reminder maintenance terpicu sesuai threshold.

---

## Phase B9 — Protocol Expansion ⬜

### Tasks
- [ ] Port & decoding protokol tambahan per referensi Traccar: Meiligao, Xexun, Suntech, H02, Totem, GT02, Navigil, Castel; validasi TK103.
- [ ] Arsitektur decoder pluggable (registrasi protokol tanpa menyentuh pipeline).
- [ ] Test vector per protokol (hex sample → struct → persist).

### Acceptance
- [ ] Device non-GT06 bisa ingest end-to-end (login→telemetry→persist→live state) tanpa perubahan service lain.

---

## Phase B10 — Normalisasi & Konfigurasi ⬜ (PRD §6.0, §14)

### Tasks
- [ ] Normalisasi prefix tabel `tm_`/`th_`/`td_` — migrasi rename idempoten (nol downtime).
- [ ] Split user master: `tm_users` (B2B) / `tm_users_b2c` (B2C) + tipe bisnis di `tm_companies`.
- [ ] Config ganda LOCAL + COOLIFY: `docker-compose.{local,coolify}.yml` + `.env.{local,coolify}`.
- [ ] Telemetry interval 20 s (default, bisa dikonfigurasi).
- [ ] Input validation + anti-attack hardening (PRD §8.5/§9.6) menyeluruh.

### Acceptance
- [ ] Migrasi rename aman dijalankan berulang; seluruh service memakai nama baru.
- [ ] Dua konfigurasi bisa di-up terpisah tanpa edit manual; interval 20 s efektif end-to-end.

---

## Phase B11 — Governance & Data Lifecycle ⬜ (PRD §6.0.1, §9.4, §14.5)

### Tasks
- [ ] Audit trail wajib `tm_audit_logs` (semua mutation endpoint menulis audit).
- [ ] Soft delete global + endpoint restore (semua entity utama).
- [ ] Auto-create admin tenant password `Admin@123` (FR-5.5) saat provisioning.
- [ ] Migrasi DB otomatis di Coolify (job/entrypoint apply migrasi saat deploy).
- [ ] Dukungan protokol universal (Module 1c) — registrasi device lintas brand.

### Acceptance
- [ ] Setiap mutation ter-audit (sampling verifikasi); restore mengembalikan data utuh.
- [ ] Provisioning tenant baru menghasilkan admin default + migrasi jalan otomatis di Coolify.

---

## Phase B12 — Enterprise & Industry Modules ⬜ (acuan `docs/FRONTEND.md`, PRD v1.7.0 §5.10)

> Penerapan backend mengikuti struktur aplikasi Frontend (`docs/FRONTEND.md`): setiap
> menu Business/Personal wajib punya dukungan data/API. Kontrak: error_code §8.1,
> RBAC row-level §3.1, soft delete §6.0.1, audit §9.4, validation §8.5; tabel baru
> prefix `tm_`/`th_`/`td_`; **additive-only**.

### Tasks
- [ ] Registry modul & menu di master (`tm_modules`, `tm_menus` — seed idempoten dari FRONTEND.md) + **role akses menu per-tenant** (`tm_role_menu_access` di company schema, seed default per role).
- [ ] Endpoint `GET /api/v1/access/menu` (menu tersedia utk user) + admin CRUD mapping (ter-audit).
- [ ] Master: **Drivers** (`tm_drivers`) & **Groups** (`tm_groups` + mapping vehicle/driver).
- [ ] Akses: **Personel**, **Kartu (RFID)**, **Log akses**.
- [ ] Aset: **Assets** registry; **Maintenance** (jadwal + reminder — menyambung B8).
- [ ] Keamanan: **Safety score** (dari B8) & **Incidents**.
- [ ] Analisis: **Reports/Analytics** lanjutan (trip & violation summary, export, scheduled).
- [ ] Administrasi: **Organization** (hierarki), **Integrations** (webhook outbound + API key), **Settings** tenant.
- [ ] **Share lokasi publik** — link token TTL + endpoint publik `GET /api/v1/share/{token}` (FR-9.3).
- [ ] **Heatmap** agregasi historis (menu Pemantauan).
- [ ] Industry-specific bertahap (per flag lisensi tenant): Rental · Transport · Logistics · Sales · Field Service · Patrol · Project Site.
- [ ] Personal/B2C: auth `tm_users_b2c`, Statistics agregasi, Settings preferensi (FR-9.2).

### Acceptance
- [ ] Navigasi frontend dimuat dinamis dari `GET /api/v1/access/menu` sesuai role.
- [ ] Setiap menu FRONTEND.md punya endpoint ber-RBAC + test (aturan coverage B4).
- [ ] Tabel baru normalisasi + audit + soft delete; migrasi idempoten.

---

## Aturan Umum Pengerjaan (clean slate)

- Urutan eksekusi: **B0 → B1 → B2 → B3 → B5a → B5b → B4 → B6 → B7** (B7 boleh paralel setelah B1), lalu **B10 → B11** + B8/B9 paralel, **B12** setelahnya.
- Frontend (F1–F4) baru boleh dimulai setelah **B0–B6 selesai** (gate PRD §20.2).
- Setiap fase: centang checklist saat item selesai **beserta bukti verifikasi nyata** (perintah, hasil, commit) — tanpa bukti tidak boleh dicentang.
- Definisi "selesai" per fase: build sukses, service boot, `/healthz` OK, data mengalir, acceptance checklist penuh.

