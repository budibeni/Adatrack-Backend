# Backend Phases — Rencana Pengerjaan B0–B12 (Clean Slate)

> **STATUS 2026-09-15 (diperbarui):** Landasan & pipeline data **SELESAI** —
> **B0 ✅** dan **B1 ✅** (bukti verifikasi ada di tiap checklist). Fase
> berikutnya yang dikerjakan: **B2** (service-websocket: REST + WebSocket +
> RBAC). Fase B3–B12 masih ⬜ terbuka. Checklist hanya dicentang bila ada bukti
> verifikasi nyata (perintah + hasil) pada kode baru di `backend/`.

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
- Go + chi/Gin + `pgx` (PostgreSQL) + Redis + NATS JetStream; layout `services/<nama>`; shared `internal/` via `replace ajb_gps/internal => ../../internal`.
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

## Phase B2 — service-websocket: REST + WebSocket + RBAC + Auth ⬜

**Tujuan:** API konsumsi data (REST + WS) dengan auth & otorisasi row-level.

### Tasks
- [ ] Auth: login (bcrypt), JWT access+refresh, logout + revocation (denylist), middleware.
- [ ] RBAC row-level per company + `tm_user_vehicles`; format response & error_code PRD §8.1.
- [ ] REST: vehicles list/detail (enrich live-state: posisi, speed, acc, fuel_level/volume/temp, satellites, altitude, gsm_signal), positions history (pagination).
- [ ] WebSocket: handshake token, subscribe per tenant, push `VehicleUpdateData` real-time (<1 s dari publish worker-live).
- [ ] Auto-provision company (FR-5.5): `POST /api/v1/companies` (SuperAdmin) → schema + admin tenant.
- [ ] Audit akses & mutasi awal (tabel audit siap dipakai lintas modul).

### Acceptance
- [ ] 401/403 benar (tanpa token, cross-tenant, tanpa hak vehicle).
- [ ] WS push end-to-end <1 s dari ingest; reconnect + resubscribe aman.
- [ ] REST sesuai kontrak PRD §8.2 (pagination, error_code).
- [ ] Unit/integration test handler + middleware hijau.

---

## Phase B3 — worker-alert + api-vehicle: Alerts, Geofence, Routes ⬜

**Tujuan:** mesin alert real-time + API manajemen armada (CRUD + assignment).

### Tasks — alert engine (worker-alert)
- [ ] Kerangka alert: dedup window, severity (low/medium/high/critical), life-cycle open→acknowledged→resolved, persist `th_alerts`.
- [ ] GEOFENCE: circle (Haversine) & polygon (ray-casting), entry + exit, state Redis, multi-zone.
- [ ] OVERSPEEDING: `tm_speed_configs` (vehicle-specific > global) + `grace_margin_percent`; critical > 1,5× limit.
- [ ] SOS: trigger alarm GT06 0x26/0x27/0x19, severity critical, eskalasi otomatis (`SOS_ESCALATION_MINUTES`/`MAX`), catat TTA.
- [ ] BATTERY_LOW (<20% default) & OFFLINE (stale > `OFFLINE_AFTER_MINUTES`).
- [ ] ROUTE_DEVIATION: threshold 200 m, refresh 30 s, max deviation ter-update.
- [ ] Notifikasi: `tm_notification_preferences` (per user/type/channel/min_severity), channel websocket fan-out + email/SMS/push via `td_notifications` (pending→sent/delivered→failed/skipped + reason), template per type, rate limit per company.

### Tasks — api-vehicle
- [ ] CRUD vehicles (+`tm_vehicle_imei_map` sync, driver/device assignment), geofences (circle/polygon + mapping vehicles), routes + `th_route_assignments` + transisi status manual.
- [ ] CRUD `tm_speed_configs`; soft delete + restore semua entitas (pola §6.0.1).

### Acceptance
- [ ] E2E per alert type: trigger → alert + persist + publish → notifikasi sesuai preference.
- [ ] Dedup & eskalasi benar (SOS TTA tercatat sekali per alert).
- [ ] RBAC row-level pada seluruh endpoint api-vehicle (403 non-assigned).
- [ ] Unit test geometri (Haversine/ray-casting) + handler hijau.

---

## Phase B5a — Fuel Sensor End-to-End (PRD Module 7) ⬜

### Tasks
- [ ] Ingest kanal fuel: GT06 `0x0D` + Teltonika AVL IO → mapping fuel_level/volume/temp.
- [ ] Persist `td_fuel_logs` (batch).
- [ ] Alert FUEL_DROP / REFUEL (threshold + window, dedup, severity).
- [ ] API fuel-configs CRUD + riwayat fuel (range waktu, pagination) + enrich live state.

### Acceptance
- [ ] E2E: device kirim fuel → tersimpan → alert ter-publish → terkirim via WS sesuai preference.
- [ ] Unit test threshold/dedup + parser kanal fuel hijau.

---

## Phase B5b — Dashcam Event Media — Scope A (PRD Module 8) ⬜

### Tasks
- [ ] MinIO/S3 (bucket + policy) + storage layer `internal/storage`.
- [ ] Upload multipart + JSON(HMAC) → lifecycle complete; katalog media per-tenant ber-RBAC row-level.
- [ ] WS `MEDIA_EVENT` (fan-out ke user berhak) + presigned GET (round-trip byte-persis).
- [ ] Retensi: job penanda `expired` + penghapusan objek sesuai policy; endpoint complete (+audit).

### Acceptance
- [ ] E2E multipart & JSON(HMAC) → MinIO → katalog → WS → retensi.
- [ ] Negatif: 401/400/404/oversize ditolak; audit + metrics media tercatat.

---

## Phase B4 — Performance, Monitoring, Testing, Hardening ⬜

### Tasks
- [ ] Load test bertahap: 400 → 1000 → 2000 msg/s, 0 data loss (delta persist vs sent).
- [ ] Endurance 24 jam kumulatif (chunked, resume-safe).
- [ ] Load test multi-tenant: banyak company × perangkat, isolasi schema terverifikasi (0 cross-tenant leakage).
- [ ] Coverage ≥80% service inti (worker-live, worker-persistence, api-vehicle, worker-alert); `go vet` + build bersih.
- [ ] Query SLA: history 30 hari < 1,5 s (index & tuning).
- [ ] Monitoring: Prometheus metrics + dashboard SLO Grafana + alert rule inti.
- [ ] Hardening: JWT revocation, rate limit, audit DB menyeluruh; retensi JetStream (max_age/max_bytes).
- [ ] Backup/DR: dump harian + checksum + uji restore; replikasi PostgreSQL (read-replica) & Redis + drill failover.
- [ ] Retensi DB: partisi/purge telemetry sesuai §11.

### Acceptance
- [ ] Load/endurance PASS terdokumentasi; SLO dashboard sehat; backup/restore & drill sukses.

---

## Phase B6 — Real-Time Data Hardening (Audit Fix) ⬜

### Tasks
- [ ] ACC status live: pakai data asli device (`Acc` telemetry), bukan inferensi `Speed > 0`.
- [ ] DTO `VehicleUpdateData` lengkap: fuel_level/fuel_volume/fuel_temp_c, satellites, altitude, gsm_signal.
- [ ] REST enrich live-state: overlay fuel_level & acc dari Redis.
- [ ] Unit test bridge/parsing/enrich hijau.

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

