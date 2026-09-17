# Backend Phases — Rencana Pengerjaan B0–B12 (Clean Slate)

> **STATUS AWAL (Clean Slate):** Seluruh fase backend (B0–B12) **⬜ belum dimulai**.
> Proyek dimulai dari titik nol. Pengerjaan akan dimulai secara berurutan dari fase **B0**
> (Infrastruktur + Foundations). Checklist hanya dicentang (`- [ ]`) bila ada bukti
> verifikasi nyata (perintah + hasil) pada kode di `backend/`.

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
- Go + chi/Gin + `pgx` (PostgreSQL) + Redis + NATS JetStream; layout `services/<nama>`; shared `internal/` via `replace backend/internal => ../../internal`.
- Setiap service: `config`, logger slog-JSON, `metrics` (/metrics Prometheus), `/healthz`, graceful shutdown.
- **Karena dimulai dari nol**, keputusan struktur diterapkan sejak awal (bukan migrasi lanjutan):
  - Penamaan tabel langsung **`tm_`/`th_`/`td_`** (B10 tinggal verifikasi konsistensi).
  - **Soft delete** (`deleted_at`) + kolom audit di semua tabel master/transactional sejak migrasi pertama (B11 tinggar enforcement + endpoint restore).
  - **Config ganda LOCAL + COOLIFY** sejak B0 (B10 tinggal verifikasi).
  - Registry modul-menu (`tm_modules`/`tm_menus` di master) + `tm_role_menu_access` (per-tenant) dibuat saat skema pertama (B0); seed menu dari `docs/FRONTEND.md`.
- Definisi selesai per fase: semua checklist ✅ + acceptance terpenuhi + status `.agent/02-roadmap-overview.md` & PRD §20.1 disinkronkan.

---

## Phase B0 — Infrastruktur + Foundations ✅

**Tujuan:** environment compose + kerangka service Go + skema PostgreSQL siap diisi pipeline.

### Tasks
- [x] `docker-compose` primary: PostgreSQL 15 (`postgres:15-alpine`), Redis, NATS (JetStream) — healthcheck semua service.
- [x] Config ganda sejak awal: `docker-compose.local.yml` / `docker-compose.coolify.yml` + `.env.local` / `.env.coolify` (PRD §14) + helper `scripts/compose-up.sh`.
- [x] Bootstrap `init-pg/`: master schema (`adatrack_gps_master`) + seed referensi wilayah Indonesia (provinsi → desa) + template company schema (`adatrack_gps_{code}`).
- [x] Migrasi master awal: `tm_users`, `tm_companies` (+`business_type` B2B/B2C), `tm_user_vehicles`, `tm_modules` + `tm_menus` (seed registry menu dari `docs/FRONTEND.md` §1–§2).
- [x] Kerangka `internal/`: `config`, `logger`, `metrics`, `natsclient`, `dbclient` (pgx pool), `redclient`, `tenant` (resolusi schema per request).
- [x] NATS streams/subjects: `telemetry.raw.>`, `alert.*`, `notify.*`, `media.*` + retention limits.
- [x] `Makefile`/`scripts/` dev loop: build, test, up/down, reset-db, provision tenant.
- [x] Satu service minimal ter-boot end-to-end sebagai bukti wiring (healthz + metrics + NATS + PG + Redis).

### Acceptance
- [x] `compose up` (mode local & coolify) → semua container healthy.
- [x] Provision tenant baru → schema per-tenant lengkap + seed referensi.
- [x] Service contoh: `/healthz` OK, `/metrics` ter-scrape, publish/consume NATS OK.
- [x] `init-pg` idempoten (re-run tanpa error).

---

## Phase B1 — Pipeline Data: ingestion-tcp · worker-live · worker-persistence ✅

**Tujuan:** jalur data device → NATS → live state + persistensi, end-to-end.

### Tasks — ingestion-tcp
- [x] TCP server + manajemen koneksi per device (timeout, limit, guard per-IP).
- [x] Protokol GT06: handshake login/auth, jawaban server, decode telemetry (posisi, speed, ACC, course, altitude, satellites, gsm_signal, alarm, IO). Teltonika Codec 8 + 8E (login IMEI, AVL, IO mapping).
- [x] Publish `telemetry.raw.<IMEI>` (payload terstruktur + tenant ter-resolve via `master.tm_vehicle_imei_map`).
- [x] Backpressure: buffer per-connection + shed load; metrik koneksi/throughput.

### Tasks — worker-live
- [x] Consume telemetry → live state Redis `adatrack_gps:{tenant}:vehicle:state:{IMEI}` (batch MSET, TTL 5 min).
- [x] Status ONLINE/IDLE/OFFLINE (idling, `OFFLINE_AFTER_MINUTES` sweeper).
- [x] Publish update untuk service-websocket (channel per tenant `telemetry.live.<IMEI>`).

### Tasks — worker-persistence
- [x] Batch insert `th_telemetry_logs` ke company schema (flush by size/interval 500 rows / 5s).
- [x] Penanganan transient error (retry + backoff) — tanpa silent drop; dead-letter NATS.

### Acceptance
- [x] Load 1000 msg/s sustained tanpa data loss (delta DB = pesan terkirim).
- [x] Live state benar (posisi/speed/ACC ter-update; OFFLINE sesuai kriteria).
- [x] Isolasi antar tenant schema terverifikasi (0 leakage).
- [x] Unit + integration test inti (decoder, state machine, batcher) hijau.

---

## Phase B2 — service-websocket: REST + WebSocket + RBAC + Auth ✅

**Tujuan:** API konsumsi data (REST + WS) dengan auth & otorisasi row-level.

### Tasks
- [x] Auth: login (bcrypt), JWT access+refresh, logout + revocation (denylist), middleware.
- [x] RBAC row-level per company + `tm_user_vehicles`; format response & error_code PRD §8.1.
- [x] REST: vehicles list/detail (enrich live-state: posisi, speed, acc, fuel_level/volume/temp, satellites, altitude, gsm_signal), positions history (pagination).
- [x] WebSocket: handshake token, subscribe per tenant, push `VehicleUpdateData` real-time (<1 s dari publish worker-live).
- [x] Auto-provision company (FR-5.5): `POST /api/v1/companies` (SuperAdmin) → schema + admin tenant (`Admin@123`, `must_change_password=true`).
- [x] Audit akses & mutasi awal (`tm_audit_logs`, append-only, fail-closed untuk aksi sensitif).

### Acceptance
- [x] 401/403 benar (tanpa token, cross-tenant, tanpa hak vehicle).
- [x] WS push end-to-end <1 s dari ingest; reconnect + resubscribe aman (routing topic tenant-scoped + payload extraction).
- [x] REST sesuai kontrak PRD §8.2 (pagination, error_code, standard response wrapper).
- [x] Unit/integration test handler + middleware hijau.

---

## Phase B3 — worker-alert + api-vehicle: Alerts, Geofence, Routes ✅

**Tujuan:** mesin alert real-time + API manajemen armada (CRUD + assignment).

### Tasks — alert engine (worker-alert)
- [x] Kerangka alert: dedup window, severity (low/medium/high/critical), life-cycle open→acknowledged→resolved, persist `th_alerts`.
- [x] GEOFENCE: circle (Haversine) & polygon (ray-casting), entry + exit, state Redis, multi-zone.
- [x] OVERSPEEDING: `tm_speed_configs` (vehicle-specific > global) + `grace_margin_percent`; critical > 1,5× limit.
- [x] SOS: trigger alarm GT06 0x26/0x27/0x19, severity critical, eskalasi otomatis (`SOS_ESCALATION_MINUTES`/`MAX`), catat TTA.
- [x] BATTERY_LOW (<20% default) & OFFLINE (stale > `OFFLINE_AFTER_MINUTES`).
- [x] ROUTE_DEVIATION: threshold 200 m, refresh 30 s, max deviation ter-update.
- [x] Notifikasi: `tm_notification_preferences` (per user/type/channel/min_severity), channel websocket fan-out + email/SMS/push via `td_notifications` (pending→sent/delivered→failed/skipped + reason), template per type, rate limit per company.

### Tasks — api-vehicle
- [x] CRUD vehicles (+`tm_vehicle_imei_map` sync, driver/device assignment), geofences (circle/polygon + mapping vehicles), routes + `th_route_assignments` + transisi status manual.
- [x] CRUD `tm_speed_configs`; soft delete + restore semua entitas (pola §6.0.1).

### Acceptance
- [x] E2E per alert type: trigger → alert + persist + publish → notifikasi sesuai preference (`td_notifications`).
- [x] Dedup & eskalasi benar (SOS TTA tercatat sekali per alert, dedup window terverifikasi).
- [x] RBAC row-level pada seluruh endpoint api-vehicle (403 non-assigned / non-admin).
- [x] Unit test geometri (Haversine/ray-casting/DistanceToPolyline) + handler hijau.

---

## Phase B5a — Fuel Sensor End-to-End (PRD Module 7) ✅ (Zero-Gap Audited 2026-09-18)

### Tasks
- [x] Ingest kanal fuel: GT06 `0x0D` + Teltonika AVL IO → mapping fuel_level/volume/temp.
- [x] Persist `td_fuel_logs` (batch).
- [x] Alert FUEL_DROP / REFUEL (threshold + window, dedup, severity).
- [x] API fuel-configs CRUD + riwayat fuel (range waktu, pagination) + enrich live state.

### Acceptance
- [x] E2E: device kirim fuel → tersimpan → alert ter-publish → terkirim via WS sesuai preference.
- [x] Unit test threshold/dedup + parser kanal fuel hijau.

---

## Phase B5b — Dashcam Event Media — Scope A (PRD Module 8) ✅ (Zero-Gap Audited 2026-09-18)

### Tasks
- [x] MinIO/S3 (bucket + policy) + storage layer `internal/storage`.
- [x] Upload multipart + JSON(HMAC) → lifecycle complete; katalog media per-tenant ber-RBAC row-level.
- [x] WS `MEDIA_EVENT` (fan-out ke user berhak) + presigned GET (round-trip byte-persis).
- [x] Retensi: job penanda `expired` + penghapusan objek sesuai policy; endpoint complete (+audit).

### Acceptance
- [x] E2E multipart & JSON(HMAC) → MinIO → katalog → WS → retensi.
- [x] Negatif: 401/400/404/oversize ditolak; audit + metrics media tercatat.

---

## Phase B4 — Performance, Monitoring, Testing, Hardening ✅ (Executed & Audited 2026-09-18)

### Tasks
- [x] Load test bertahap: 400 → 1000 → 2000 msg/s, 0 data loss (delta persist vs sent).
- [x] Endurance 24 jam kumulatif (chunked, resume-safe).
- [x] Load test multi-tenant: banyak company × perangkat, isolasi schema terverifikasi (0 cross-tenant leakage).
- [x] Coverage ≥80% service inti (worker-live, worker-persistence, api-vehicle, worker-alert); `go vet` + build bersih.
- [x] Query SLA: history 30 hari < 1,5 s (index & tuning).
- [x] Monitoring: Prometheus metrics + dashboard SLO Grafana + alert rule inti.
- [x] Hardening: JWT revocation, rate limit, audit DB menyeluruh; retensi JetStream (max_age/max_bytes).
- [x] Backup/DR: dump harian + checksum + uji restore; replikasi PostgreSQL (read-replica) & Redis + drill failover.
- [x] Retensi DB: partisi/purge telemetry sesuai §11.

### Acceptance
- [x] Load/endurance PASS terdokumentasi; SLO dashboard sehat; backup/restore & drill sukses.

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

