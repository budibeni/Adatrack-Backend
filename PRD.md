# Product Requirement Document (PRD)
## Real-Time GPS Tracking & adatrack Management System

---

> **⚠️ DOKUMEN MASTER — SINGLE SOURCE OF TRUTH**
> File ini adalah satu sumber kebenaran. Untuk detail yang tidak diulang di sini, referensi
> langsung menunjuk ke dokumen sumber (TOC di §0.3).

## 📑 Table of Contents

| § | Section | Content |
|---|---|---|
| **0** | Metadata & Sumber Dokumen | Properti, tech stack, source map |
| **1** | Dokumen Overview | Metadata dokumen |
| **2** | Executive Summary & Objectives | Problem, vision, OKRs |
| **3** | User Personas & Roles | 4 roles + RBAC model + platform tier |
| **4** | System Architecture & Data Flow | Diagram, multi-tenant, alur data E2E |
| **5** | Functional Requirements (FR) | Module 1–9 (Module 9 = acuan `docs/FRONTEND.md`) + **registry protokol universal (200+ Traccar)** + feature specs |
| **6** | Database Schema | Master + company DB (PostgreSQL) + normalisasi `tm_`/`th_`/`td_` + user B2B/B2C + **soft delete** |
| **7** | Configuration Management | LOCAL + COOLIFY, env vars, migrasi otomatis, replica |
| **8** | API & WebSocket Contracts | Response/error format, endpoints, WS events, input validation |
| **9** | Auth, Authorization & Security | JWT, RBAC, **audit trail wajib**, TLS, anti-attack hardening |
| **10** | Monitoring & Observability | Metrics, healthz, alerting, notification delivery |
| **11** | Data Retention & Archival | Tier + purging + media + JetStream + audit/soft-deleted |
| **12** | Disaster Recovery & Backup | Backup, restore, DR runbook |
| **13** | High Availability & Replication | Replica per-engine, read/write split |
| **14** | Deployment & DevOps | Topologi, ports, build, CI/CD, migrasi otomatis Coolify |
| **15** | Compliance & Privacy | GDPR, encryption, access logging |
| **16** | Testing Strategy & Quality | Layer coverage, tooling |
| **17** | Non-Functional Requirements | SLA table |
| **18** | Risk Assessment & Mitigation | Risk matrix |
| **19** | Success Criteria | Acceptance criteria |
| **20** | Implementation Phases & Roadmap | B0–B11, F1–F4, lintas-fase fitur |
| **21** | Known Gaps & Future Enhancements | Audit + roadmap gap |
| **22** | Appendix | NATS subjects, glossary, doc index |

---

## 0. Metadata & Sumber Dokumen

### 0.1 Properti Dokumen

| Properti | Nilai |
|---|---|
| **Judul** | Real-Time GPS Tracking & adatrack Management Platform |
| **Versi Konsolidado** | **v1.7.0 (2026-09-15)** — v1.6.0 (2026-09-13): integrasi PRD v1.3.0 + GAPS + FEATURE + dokumen terkait; tipe bisnis B2B/B2C, normalisasi tabel `tm_`/`th_`/`td_`, telemetry 20 s, 2 konfigurasi (local & Coolify), **audit trail wajib**, **soft delete global**, **migrasi otomatis Coolify**, **dukungan universal semua brand GPS (referensi Traccar)**, keamanan & validasi diperkuat; **v1.7.0: struktur aplikasi Frontend (Business & Personal) mengacu `docs/FRONTEND.md` — penerapan backend mengikuti struktur tsb (§4.2, §5.10 Module 9, §20)** |
| **Status** | Approved for Engineering — **clean slate 2026-09-15: sistem dibangun dari ulang, seluruh fase backend (B0–B12) selesai**; frontend F1–F4 ⬜ (gate: B0–B6) |
| **Tipe Bisnis** | **B2B (multi-tenant — default)** · B2C (single-tenant konsumen, rencana) — lihat §4.2 |
| **Kompatibilitas Device** | **Semua brand/model GPS tracker tanpa terkecuali** — protokol & port mengikuti referensi Traccar (200+ protokol / 2000+ model); **Teltonika dikecualikan** (referensi protokol sendiri) — lihat Module 1c |
| **Target Audience** | Engineering Team (Backend, Frontend, DevOps), Product Managers, QA Team, System Architects |
| **Target SLA** | Latensi < 800 ms · Throughput 2000 msg/s peak · Uptime 99.9% · Coverage ≥ 80% · Telemetry interval 20 s |

### 0.2 Tech Stack

| Layer | Teknologi |
|---|---|
| Ingestion (TCP + parse protokol) | Golang (`ingestion-tcp`) |
| Message Broker / Queue | NATS (JetStream `-js`, retensi 48h/4GiB) |
| Live State / Cache | Redis |
| Persistent DB | **PostgreSQL** (schema-per-tenant) |
| Object Storage (media dashcam) | MinIO (dev, S3-compatible) / AWS S3 / OSS (prod) |
| Real-Time Streaming | Golang WebSocket (gorilla/websocket) |
| REST API | Golang, Gin (`api-vehicle`, `service-websocket`, `service-media`) |
| Frontend | Next.js (React), TailwindCSS, Mapbox GL / Leaflet — struktur aplikasi (Business & Personal + `packages/` shared) mengacu `docs/FRONTEND.md` |

### 0.3 Sumber yang Dikonsolidasikan (Map Dokumen)

| Dokumen Sumber | Konten yang Dikonsolidasikan | Referensi Detail |
|---|---|---|
| `PRD.md` | **Canonical v1.6.0 (konsolidasi)** — PRD v1.3.0 + GAPS #1–14 + FEATURE matrix + dokumen terkait; FR Module 1–8, schema §6, config §7, monitoring §8, NFR/risiko/kriteria §10–12 | `PRD.md` |
| `docs/DATABASE_ARCHITECTURE.md` | Decisyon DB single-instance vs split; read/write split | `docs/DATABASE_ARCHITECTURE.md` |
| `docs/HIGH_AVAILABILITY.md` | HA & DR strategi, replika (baca), failover Redis | `docs/HIGH_AVAILABILITY.md` |
| `docs/INCIDENT_RUNBOOK.md` | Runbook insiden (CPU/mem/RDS/stuck/dll) — **baca saat insiden** | `docs/INCIDENT_RUNBOOK.md` |
| `docs/POSTGRES_PROVIDER.md` | Provider Postgres implementasi + limitasi | `docs/POSTGRES_PROVIDER.md` |
| `docs/FRONTEND.md` | **Struktur aplikasi Frontend (v1.7.0)** — modul/menu/fitur aplikasi **Business** (§1: Utama, Master Data, Akses, Aset & Perawatan, Keamanan, Analisis & Laporan, Industry Specific, Administrasi) & **Personal** (§2) + fitur lintas aplikasi (§3) | **Acuan penerapan backend** — §4.2, §5.10 (Module 9), §8.2, §20 |
| `docs/device-connection-guide.md` | Panduan koneksi device GPS ke aplikasi | `docs/device-connection-guide.md` |
| `.agent/01–04` | Global rules, roadmap overview, phases detail | `.agent/` |
| `PROGRESS_TRACKING.md` | Status fases historis + rework multi-tenant | `PROGRESS_TRACKING.md` |

---
## 1. Dokumen Overview & Metadata

* **Project Title:** Real-Time GPS Tracking & adatrack Management Platform
* **Document Version:** Konsolidado v1.6.0 (2026-09-13) — integrasi PRD v1.3.0 + GAPS + FEATURE + dokumen terkait
* **Status:** Approved for Engineering
* **Target Audience:** Engineering Team (Backend, Frontend, DevOps), Product Managers, QA Team, System Architects
* **Target SLA:** 5.000+ device GPS · telemetry interval **20 detik** · 2.000 msg/s peak · uptime 99.9% · ≤50 tenant

---

## 2. Executive Summary & Objectives

### 2.1 Problem Statement
Perusahaan pengelola armada kendaraan (logistik, rental, transportasi publik) membutuhkan sistem
pemantauan posisi dan status armada secara real-time dengan latensi sangat rendah (< 1 detik).
Banyak sistem legacy mengalami bottleneck saat menangani ribuan perangkat GPS yang mengirimkan
data telemetry secara bersamaan. Pada platform ini interval telemetry dinormalisasi ke
**20 detik per device** (5.000 device → ~250 msg/s nominal), dengan toleransi server terhadap
burst lebih cepat (batch/rate-limit) dan bucket *peak* 2.000 msg/s (lihat FR-1.2/FR-4.1).

### 2.2 Product Vision
Membangun platform *Real-Time adatrack Management* berkinerja tinggi, berskala besar (*scalable*),
 dan *fault-tolerant* yang mampu mencerna (*ingest*) puluhan ribu paket data GPS per detik,
menampilkan pergerakan armada secara *smooth* di peta, serta memberikan notifikasi bahaya/geofence
secara *real-time*.

### 2.3 Key Success Metrics (OKRs)

| Metric | Target | Notes |
|--------|--------|-------|
| **Telemetry Latency** | < 800 ms | End-to-end: GPS device send → Dashboard display |
| **System Uptime** | 99.9% | = ~8.64 hours downtime per year |
| **Peak Device Capacity** | 5.000+ total (across all companies) | Message frequency: **every 20 seconds** |
| **Number of Companies** | ≤50 concurrent tenants | Multi-tenant: schema per tenant (`adatrack_gps_{code}`) — **tipe bisnis B2B** |
| **Dashboard Users** | 500–1.000 concurrent users | Role-based access (not all see all vehicles) |
| **Database Query Performance** | < 1.5 seconds | Playback 30 days of history (diukur saat B4) |
| **Throughput** | 2.000 msg/s (peak capacity) | Nominal 5.000 device × 1 msg/**20 detik** = 250 msg/s; peak/stress 2.000 msg/s target 0 loss (B4) |
| **Resource Stability** | 0 memory-leak / bottleneck saat peak | p50 heap stabil, goroutines plateau, p95 latency < 800 ms (lih. FR-4.4) |
| **Device Compatibility** | **Semua brand GPS** (universal) | Protokol + port dari referensi Traccar (200+); Teltonika referensi sendiri (Module 1c) |
| **Audit Trail** | 100% event keamanan & mutasi data tercatat | `tm_audit_logs` append-only, no silent drop (§9.4) |
| **Data Deletion** | 100% **soft delete** | `deleted_at` + restore endpoint; hard delete hanya job retensi/GDPR (§6.0.1) |

---
## 3. User Personas & Roles

| Role | Description | Core Goals | Vehicle Access | Dashboard Users |
| :--- | :--- | :--- | :--- | :--- |
| **System Admin** | Mengelola tenant, akun pengguna, pendaftaran unit GPS baru. | Konfigurasi sistem, manajemen user & permission. | **ALL 5000+ vehicles** | > 50 |
| **adatrack Manager** | Memantau armada, analisis efisiensi rute & konsumsi BBM. | Monitoring ketersediaan, laporan mingguan. | **Assigned adatrack (50-500)** | 500-2000 |
| **Dispatcher/Operator** | Memantau pergerakan armada real-time. | Menanggapi alerts, geofence breach, emergency. | **Assigned adatrack (50-500)** | 1000-2000 |
| **Driver** | Pengemudi kendaraan dengan GPS tracker. | Menerima rute, SOS darurat. | **Own vehicle only** | 1000-1500 |

### 3.1 Role-Based Access Control (RBAC) Model

**Access Control Strategy:**
- **Database Layer:**
  - `tm_users` table (master): Stores user credentials + global role (`SuperAdmin`, `Admin`,
    `adatrack_MANAGER`, `OPERATOR`, `DRIVER`).
  - `tm_user_company_access` (company DB): registry user lokal — `role_override` per company,
    `is_active`, `permissions` JSON. `NULL` role_override = pakai role global.
  - `tm_user_vehicles` junction table: Maps user → vehicles they can access (company DB).
  - Every query filtered by: `WHERE vehicle_id IN (user's assigned vehicles)` (row-level).

- **API Layer:**
  - Every endpoint must validate JWT token + tenant context + user role.
  - Check `tm_user_vehicles` before returning vehicle data; cross-tenant → **403**.
  - Platform-only endpoints (`/companies`, `/users`) → isPlatformAdmin guard; tenant identity → **403 PLATFORM_ONLY**.

- **WebSocket Layer:**
  - AppHub fan-out filter per company + per vehicle permission (hub.canReceive).
  - Subscribe `vehicle.update.{vehicle_id}` / receive `VEHICLE_UPDATE` only for authorized vehicles.
  - Reject `UNAUTHORIZED_VEHICLE` (403) bila tidak diizinkan.

**Benefits:**
- Reduces broadcast load (not all users see all 5000 vehicles)
- Improves latency (smaller message fan-out)
- Increases security (users only see their own vehicles)
- Simplifies dashboard queries (pre-filtered by role)

**Platform Tier (governance, B2 2026-08-24):**
- Konteks `default` (company registry `'DEFAULT'` → `adatrack_gps_default`) + role `SuperAdmin`
  = identitas platform. Token platform hanya bisa akses endpoint platform (`/companies`, `/users`);
  request ke route tenant → `403 PLATFORM_SCOPE`.
- Akun dev: `platform@adatrackgps.local` / `Platform@123` (seed).

---
## 4. System Architecture & Data Flow

```text
┌─────────────────────────────────────────────────────────────────┐
│                  GPS DEVICES (≤5.000 total, ≤50 companies)                    │
│            Sending 1 message every 20 seconds                    │
└────────────────────────┬────────────────────────────────────────┘
                         │ TCP (multi-port: GT06 :9000 · Teltonika :9011 · TK103 :9002)
                         │
         ┌───────────────▼─────────────────┐
         │  ingestion-tcp (1 instance)     │
         │  - Accept 5000 connections      │
         │  - Parse binary protocol        │
         │  - Anti-spoofing: IMEI allowlist │
         │    (master.tm_vehicle_imei_map)    │
         │  - Enrich company_code + pub    │
         └───────────────┬─────────────────┘
                         │ NATS pub: telemetry.raw.<IMEI>
         ┌───────────────▼──────────────────┐
         │  NATS JetStream (1 instance)    │
         │  - MaxPending: 10.000 msgs      │
         │  - Retensi 48h / 4 GiB per stream│
         └──────┬──────────────┬───────────┘
                │              │
    NATS Sub    │              │ NATS Sub
         ┌──────▼──┐      ┌────▼─────────┐
         │worker-  │      │worker-       │
         │live     │      │persistence   │
         │(Redis)  │      │(DB Batch)    │
         └──────┬──┘      └────┬─────────┘
                │              │
         ┌──────▼──────────────▼───────┐
         │  Redis (live state)         │
         │  PostgreSQL                 │
         │  ┌─ master (auth+IMEI map)  │
         │  └─ per-company (schema/DB) │
         └──────┬──────────────────────┘
                │
    ┌───────────▼─────────────────┐
    │service-websocket (1)        │
    │ - REST API + WebSocket      │
    │ - RBAC + tenant routing     │
    ├ service-media (media events)│
    │ api-vehicle (vehicle mgmt)  │
    │ worker-alert (alerts/rules) │
    └───────────┬─────────────────┘
                │
    ┌───────────▼──────────────────────────────┐
    │  DASHBOARD USERS (500–1.000)            │
    │  Admin (all vehicles) / Managers /      │
    │  Operators (assigned) / Drivers (own)   │
    └──────────────────────────────────────────┘
```

> **Multi-Tenant Notes:**
> - **Master DB** (`adatrack_gps_master`): reference data (countries/provinces/cities/districts/
>   subdistricts, vehicle_categories/types), `tm_users` (**auth authority**, bcrypt), `tm_vehicle_imei_map`
>   (IMEI → company), tm_audit_logs, tm_company_media_config.
> - **Company DB** (`adatrack_gps_{LOWER(company_code)}` — schema PostgreSQL via search_path):
>   tm_user_company_access, vehicles, tm_user_vehicles, th_telemetry_logs (partitioned), th_fuel_logs,
>   geofences, alerts, tm_speed_configs, tm_geofence_vehicles, tm_notification_preferences, notifications,
>   routes, th_route_assignments, th_media_events.
> - **Tenant resolution:** ingestion lookup IMEI via `master.tm_vehicle_imei_map` (company belum diketahui).
> - **No super-admin cross-tenant:** admin diper-company via `tm_user_company_access.role_override`.
> - **DB Engine:** PostgreSQL (schema per tenant) — akses DB melalui paket `internal/dialect`.
> - **Read replica:** query baca → replika (`ReadPool`/`ReadRouter`, fallback primary); tulis → primary.

### 4.1 Alur Data (end-to-end)

1. Device GPS → TCP binary frame (GT06/Teltonika/TK103) → `ingestion-tcp`.
2. Parse + anti-spoofing (IMEI allowlist) + enrich `company_code`/`vehicle_id`.
3. Publish JSON → NATS `telemetry.raw.<IMEI>`.
4. `worker-live` (queue `live`) → Redis `adatrack_gps:{company}:vehicle:state:<IMEI>` (TTL 5 min) + odometer/engine-hours + trip/stop detection.
5. `worker-persistence` (queue `persistence`) → batch insert (500 rows / 5 s) → `th_telemetry_logs`/`th_fuel_logs`.
6. `worker-alert` (queue `alert`) → deteksi GEOFENCE/OVERSPEED/BATTERY/OFFLINE/SOS/ROUTE_DEVIATION/FUEL → `th_alerts` + publish `alert.*` + notifikasi.
7. `service-websocket` (queue `websocket`) → fan-out `VEHICLE_UPDATE`/`MEDIA_EVENT`/`notify.alert.*` ber-RBAC ke dashboard.

### 4.2 Tipe Bisnis: B2B (multi-tenant) vs B2C (single-tenant)

Platform dibedakan berdasarkan **tipe bisnis operasional**:

| Aspek | **B2B** (default proyek) | **B2C** (rencana) |
|---|---|---|
| **Model tenant** | **Multi-tenant** — 1 tenant per customer/company; ≤50 concurrent | Single-tenant — satu instance platform konsumen |
| **Penyimpanan** | Schema PostgreSQL per tenant (`adatrack_gps_{company_code}`) + master schema | Schema/DB konsumen terpisah (isolasi penuh) |
| **Autentikasi user** | `tm_users` (master) + `tm_user_company_access` (per tenant) | `tm_users_b2c` (master tipe bisnis B2C) |
| **Relasi data** | company/tenant → tm_user_company_access → vehicles | end-user → owned device/vehicle (personal) |
| **RBAC** | SuperAdmin/Admin/Manager/Operator/Driver per tenant | User + perangkat (lebih ringkas) |
| **Monetisasi/RIS** | Kontrak enterprise, SLA tertulis | Langganan individu |

> **Kebijakan:** Implementasi saat ini = **tipe bisnis B2B (multi-tenant)**. Seluruh arsitektur
> (auth master, tenant routing, RBAC, auto-provision) dirancang untuk B2B. Tipe **B2C** tidak
> mengubah pipeline telemetry/alert/geofence — hanya model *tenant*, tabel *user*, dan scope RBAC
> yang berbeda. Ke depan B2C dapat berjalan di schema terpisah dengan `tm_users_b2c` tanpa
> regresi B2B (normalisasi penamaan tabel diterapkan, lihat §6.0).

**Pemetaan aplikasi Frontend (v1.7.0 — `docs/FRONTEND.md`):** tipe bisnis **B2B** →
**Aplikasi ADATRACK Business** (menu FRONTEND.md §1: Utama, Master Data, Akses, Aset &
Perawatan, Keamanan, Analisis & Laporan, Industry Specific, Administrasi); tipe bisnis
**B2C** → **Aplikasi ADATRACK Personal** (menu FRONTEND.md §2: Tracking, Statistics,
Settings). **Penerapan backend mengikuti struktur tsb** — setiap menu wajib punya dukungan
data/API backend; rincian cakupan per menu → §5.10 (Module 9) + fase **B12** (§20.1).

#### 4.2.1 Provisioning Tenant Otomatis + Admin Tenant Default (FR-5.5)

Saat SuperAdmin membuat company/tenant baru (`POST /api/v1/companies`), sistem **otomatis**
membuat **satu akun Admin tenant** untuk perusahaan tersebut — onboarding selesai tanpa
langkah manual:

| Item | Nilai |
|---|---|
| **Username/email** | `admin@{company_code}.local` (dapat di-override lewat body `admin_email`) |
| **Password default** | **`Admin@123`** (bcrypt cost 12; **tidak pernah** disimpan plaintext / tidak di-log) |
| **Role** | `Admin` (scope tenant tsb) + baris `tm_user_company_access` |
| **Flag wajib** | `must_change_password = true` — login pertama **wajib** ganti password; endpoint lain → `403 PASSWORD_CHANGE_REQUIRED` sampai diganti |
| **Audit** | `COMPANY_CREATED` + `TENANT_PROVISIONED` + `USER_CREATED` (§9.4) + `password_reset` event |
| **Idempotensi** | Pembuatan ulang company yang sama → tidak menimpa password admin; hanya melaporkan admin yang ada (`409`/`admin_exists`) |
| **Keamanan** | Password default hanya berlaku sampai login pertama; percobaan gagal berulang mengikuti rate-limit/lockout login (§9.2); notifikasi email opsional (SMTP) |

> **Rasional:** mempercepat onboarding enterprise (B2B) sekaligus menutup risiko "admin
> tertinggal" — dengan tetap memaksa rotasi password. FR-5.5 versi lama (tanpa pembuatan
> admin) dianggap tidak lengkap.

---
## 5. Functional Requirements (FR)

### Module 1: Ingestion & Device Communication (Go TCP + NATS)

**FR-1.1:** TCP listener (port `TCP_PORT`, default 9000), max 5.000 koneksi concurrent.
**Dukungan universal semua brand GPS:** setiap protokol mendapat listener sendiri dengan port
default **mengikuti konvensi referensi Traccar** (mis. GT06 5001 → `TCP_PORT`; Meiligao 5002 →
`PORT_MEILIGAO_TCP`; Xexun 5003 → `PORT_XEXUN_TCP`; Suntech 5017 → `PORT_SUNTECH_TCP`; H02 5010 →
`H02_TCP_PORT`) — semuanya overridable via env; `0` = listener nonaktif.
**Teltonika dikecualikan dari konvensi Traccar:** memakai referensi protokol sendiri
(`TELTONIKA_TCP_PORT`=9011 → `docs/docs-device/traccar-reference/08-teltonika-codec8.md`).
Detail tabel protokol+port & checklist onboarding: **Module 1c**.

**FR-1.2:** Parser protokol → JSON standar payload NATS dengan field:
`imei, lat, lon, speed, heading, altitude, satellites, acc (ACC riil dari tracker),
battery, gsm_signal, timestamp, company_code, vehicle_id` + opsional `fuel_level/fuel_volume/
fuel_temp_c`, `mileage/odometer`.

> **Interval telemetry default = 20 detik per device** (5.000 device → ~250 msg/s nominal;
> peak bucket 2.000 msg/s). Update yang datang lebih cepat dari 20 detik tetap diproses,
> tetapi dapat di-batch/rate-limit server-side (FR-4.1/FR-4.2). Nilai interval dapat
> di-override per device via downlink (B8); server memakai `TELEMETRY_INTERVAL_SECONDS`
> (default `20`) untuk perhitungan buffer/health-check.

**FR-1.3:** Kirim ACK keep-alive (login position), timeout idle 90s / offline 3 menit.

**FR-1.4 (Anti-spoofing):** HANYA IMEI terdaftar di `master.tm_vehicle_imei_map` yang diproses;
IMEI tak dikenal → tolak + structured log + counter `tenant_lookup_errors_total`.

**FR-1.5:** Backpressure: bila NATS pending > 50% → warn; > 90% → drop + log error
(metrik `backpressure_drops_total`).

**FR-1.6:** Publish `telemetry.raw.<IMEI>` via `NATSClient.Subject()` helper
(prefix `NATS_SUBJECT_PREFIX`, default `telemetry`).

#### Module 1a: GT06 / Concox Protocol — Implementasi & Catatan Jujur

Sumber acuan: `docs/docs-device/GT06_GPS_Tracker_Communication_Protocol_v1.8.1.md` +
`docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md`.

- **Framing** `0x78 0x78` (len 1 B) & `0x79 0x79` (len 2 B); **CRC-ITU (CRC-16)** tabel
  Appendix v3.1 (terverifikasi vs 4 contoh biner: `D9DC`, `8CDD`, `E1A0`, `9FF8`).
- **Login** `0x01` → IMEI 15-digit + tenant resolution.
- **Position** `0x12`/`0x22`: date-time, satellites (nibble), lat/lon = raw ÷ 1.800.000,
  speed, course 10-bit, hemisphere; tail v3.1 defensif (ACC, mileage 4B bila tersedia).
- **Heartbeat** `0x13`/`0x23`; **Alarm** `0x26`/`0x27`(HVT)/`0x19`(LBS); **Time check** `0x8A`.
- ⚠️ **Encoding tanggal:** plain-hex default (konform contoh dokumen), toggle `GT06_DATE_BCD=true`
  untuk firmware BCD (catatan jujur: kontradiktif dalam dokumen vendor).
- ⚠️ Verifikasi packet capture device nyata masih dilacak sebelum produksi (catatan jujur).
- Model terdaftar (v3.1): JM-VL02/VG03/VG04, EG02/EG03, JM01, JV200, GT300, GT800, MT200, OB22, X3, Q2, GT08, Wetrack lite, ET25, HVT001.

#### Module 1b: Teltonika — Referensi Protokol Sendiri, PRIORITAS TINGGI

> **Teltonika TIDAK mengikuti konvensi Traccar.** Perangkat ini memakai **referensi protokol
> sendiri (standalone)** karena Codec 8 Extended mendukung IO ID space 2-byte (65535 ID) —
> diperlukan untuk fuel sensor & telemetri lanjutan. Referensi:
> `docs/docs-device/traccar-reference/08-teltonika-codec8.md`.

**Teltonika (Codec 8 `0x08` / Codec 8 Extended `0x8E`):**
- Login: 4-byte length + IMEI ASCII → balas `0x01`.
- AVL packet: `4B len | CodecID | count | records | CRC-16(LE IBM 0xA001) | count`.
- Codec 8E record (PRIORITAS TINGGI): **base 28 byte** (timestamp 8 + priority 1 + GPS 15 +
  Event IO ID 2 + N Total IO 2) + 5 count grup 2-byte (N1/N2/N4/N8/NX). Codec 8: base 26 byte +
  count grup 1-byte. Dispatch per-codec: `0x08` → parseCodec8Record, `0x8E` → parseCodec8ERecord.
- IO mapping: 72→battery, 66/67→ACC, fuel 86/87/84 (via env `TELTONIKA_IO_FUEL_*`).
- Balas jumlah record (codec-8 convention).
- 📘 **Referensi standalone:** `docs/docs-device/traccar-reference/08-teltonika-codec8.md`
  (struktur frame TCP/UDP, AVL record, IO elements, contoh packet hex, kode Go).
- ⚠️ **Codec 7 (0x07) — DROPPED (2026-09-06)** — tidak pernah diverifikasi device nyata,
  altitude offset diasumsikan; risiko silent data corruption. Detail: `docs/CODEC7_DECISION.md`.
- ⚠️ Fuel/GSM IO & TK-103 offset belum diverifikasi device nyata (jujur).

**TK103 (provisional):** subset frame GT-clone; belum divalidasi ke device fisik (jujur).

#### Module 1c: Dukungan SEMUA Brand GPS (Universal Protocol Support)

> **Kebijakan: platform menerima SEMUA merek/model GPS tracker tanpa terkecuali.** Selama
> perangkat berbicara protokol yang terdaftar pada referensi Traccar (200+ protokol, 2000+
> model tracker), perangkat tersebut dapat di-onboard — **tidak ada pembatasan merek**.

- **Sumber kebenaran protokol & port:** `docs/docs-device/traccar-reference/` — decoder Traccar
  (`src/main/java/org/traccar/protocol/`) dipakai sebagai acuan struktur frame, command, ACK,
  dan **nomor port default**.
- **Satu-satunya pengecualian:** **Teltonika** memakai referensi sendiri (Module 1b,
  `08-teltonika-codec8.md`) karena kompleksitas Codec 8/8E dan statusnya prioritas tertinggi.
- **Port mengikuti konvensi Traccar** — satu listener per protokol, semua overridable via env:

| Protokol | Port default (Traccar) | Env Var | Status |
|---|---|---|---|
| GT06 (Concox) | 5001 | `TCP_PORT` | ⬜ B0 (prioritas utama) |
| **Teltonika** | 5027 | `TELTONIKA_TCP_PORT` | ⬜ B0 — **referensi sendiri** (bukan Traccar) |
| TK103 | 5013 | `TK103_TCP_PORT` | ✅ B9 (validasi) |
| Meiligao (GT30i/GT60/VT300) | 5002 | `PORT_MEILIGAO_TCP` | ✅ B9 (prioritas tinggi) |
| Xexun (GPS103/GPS303) | 5003 | `PORT_XEXUN_TCP` | ✅ B9 (prioritas tinggi) |
| Suntech (ST215/ST240/ST340) | 5017 | `PORT_SUNTECH_TCP` | ✅ B9 (prioritas tinggi) |
| H02 / H08 | 5010 | `H02_TCP_PORT` | ✅ B9 (prioritas tinggi) |
| Totem | 5005 | `PORT_TOTEM_TCP` | ✅ Done (prioritas sedang) |
| GT02 / GT02A | 5006 | `PORT_GT02_TCP` | ✅ Done (prioritas sedang) |
| Navigil | 5012 | `PORT_NAVIGIL_TCP` | ✅ Done (prioritas sedang) |
| Castel (SC/CC/MPIP) | 5019 | `PORT_CASTEL_TCP` | ✅ Done (prioritas sedang) |
| CalAmp / Cellocator / Ruptela | lihat `04-priority-low.md` | `<PROTOCOL>_TCP_PORT` | ⬜ Backlog (prioritas rendah) |
| *Protokol Traccar lainnya (200+)* | lihat `07-appendix.md` | `<PROTOCOL>_TCP_PORT` | ⬜ onboarding bertahap |

- **Alokasi port (konvensi Traccar):** `5000–5099` TCP listener protokol · `5100–5199` UDP ·
  `5200–5299` HTTP-based protocol. Proyek dapat memakai override dev (mis. `9000`/`9003`)
  selama tidak bentrok antar-listener — guard boot menolak port bentrok (`os.Exit(1)`).
- **Onboarding protokol baru (checklist):** (1) dokumentasikan referensi decoder ke
  `docs/docs-device/traccar-reference/`; (2) implementasi `FrameDecoder`/parser + ACK sesuai
  referensi; (3) alokasikan port dari tabel konvensi Traccar + env `*_TCP_PORT`;
  (4) tulis golden test frame 1:1 dari contoh referensi; (5) registrasi IMEI (anti-spoofing
  tetap berlaku); (6) verifikasi E2E (frame → `telemetry.raw.<IMEI>` → DB + Redis + WS).
- **Anti-spoofing tetap universal:** semua brand mengikuti allowlist `tm_vehicle_imei_map`;
  IMEI tak dikenal ditolak + audit `IMEI_REJECTED` (§9.4) — ini **bukan** berarti brand
  tidak didukung, hanya perangkat belum diregistrasi.

#### Module 1d: Referensi Protokol (index)

- **Universal — 200+ protokol / 2000+ model:** `docs/docs-device/traccar-reference/` —
  `01-overview.md` (kategori & gap analysis), `02-priority-high.md` + `02b–02d-*.md`
  (Meiligao/Xexun/Suntech/H02), `03-priority-medium.md`, `04-priority-low.md`,
  `05-implementation-guide.md`, `06-full-protocol-list.md` (daftar lengkap A–Z),
  `07-appendix.md` (**tabel port default Traccar** + checksum/BCD helper).
- **Teltonika — standalone (prioritas tertinggi):** `08-teltonika-codec8.md`.
- **GT06 vendor:** `docs/docs-device/GT06_GPS_Tracker_Communication_Protocol_v1.8.1.md` +
  `docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md`.
- Referensi tambahan: `PRD.md` Module 1d + `.agent/01-global-rules.md` §Referensi Protokol.

---
### Module 2: State Management & Caching (Redis)

**FR-2.1:** Live state Redis hash:
```
Key: adatrack_gps:{company_code}:vehicle:state:<IMEI>
Value: { lat, lon, speed, heading, acc, status, fuel_level, fuel_temp_c,
         satellites, altitude, gsm_signal, battery, timestamp }
TTL: 5 minutes (offline detection)
```
> Tenant isolation: key prefix `adatrack_gps:{company_code}:` mencegah collision antar company.

**FR-2.2:** Status koneksi: **Online** (last msg < 90 s) · **Idle** (90 s–3 min) · **Offline** (> 3 min).

**FR-2.3 (Batch):** Buffer 100 msg → 1 MGET + 1 MSET per 100 ms (100x reduction Redis ops);
fast-path MSET utk pesan berposisi; pesan fuel-only partial **merge** (posisi tidak tertimpa).

**FR-2.4 (Pooling):** Min 10 / Max 30 / Timeout 5 s / Idle 5 min.

**FR-2.5 (B7.1 — Odometer & Engine Hours):** Akumulasi delta Haversine (radius 6371 km) per
position packet → `odometer_km`; akumulasi `engine_hours` solo saat ACC ON. Guard: skip
fuel-only, VehicleID=0, delta > 5 km (GPS jump), interval terlalu lama. Flush 30 s / ≥100 vehicle.
Metrik `odometer_updates_total`, `engine_hours_updates_total`.

**FR-2.6 (B7.2 — Trip & Stop Detection):** State machine MOVING ↔ STOPPED per vehicle,
grace `TRIP_STOP_GRACE_SECONDS` (30 s), min stop `TRIP_MIN_STOP_SECONDS` (60 s), auto-close
`TRIP_MAX_STOP_SECONDS` (3600 s) → tabel `th_vehicle_trips` / `td_vehicle_stops`. Metrik
`trip_events_total{company_code,event_type}`, `stop_events_total{company_code}`.

---

### Module 3: Storage & Persistence Engine (PostgreSQL)

**FR-3.1:** Bulk/Batch Insert dari NATS → DB tiap 5 detik ATAU per 500 records (whichever first)
— interval insert **tidak terikat interval telemetry (20 s)**, batch tetap mengejar throughput.
Ack batch sukses; NACK + retry exponential backoff (1s/5s/10s, max 3); bila retries habis →
publish `telemetry.error.<IMEI>` + log (no silent drop). Graceful shutdown: drain batch & settle ack.

**FR-3.2:** `th_telemetry_logs` di-partition RANGE (timestamp) bulanan — query playback cepat,
purge data lama, reduce lock contention.

**FR-3.3 (Pooling):** Min 20 / Max 50 / Max Idle 5 min / Statement Timeout 30 s.

**FR-3.4 (Batch Algorithm):**
```
1. Read up to 500 messages from NATS (or wait 5 s)
2. Prepare batch INSERT (prepared statement; per-company routing)
3. Execute INSERT INTO th_telemetry_logs (...) VALUES (...) [500x]
4. Ack all 500  |  5. Fail → NACK + backoff (1s,5s,10s)
6. Retries exhausted → error queue + log
```
Routing fuel rows → `th_fuel_logs`; fuel-only tanpa posisi TIDAK masuk `th_telemetry_logs`
(counter `fuel_rows_positionless_total`). ACC status tersimpan ke `acc_status` (fix B5a).

**FR-3.5 (Indices — database reference):**
```sql
PRIMARY KEY (id, timestamp)
CREATE INDEX idx_device_time ON th_telemetry_logs (imei, timestamp DESC);
CREATE SPATIAL INDEX idx_location ON th_telemetry_logs (location);
CREATE INDEX idx_timestamp ON th_telemetry_logs (timestamp);
-- Query SLA target (diukur di B4): history 30 hari < 1,5 s (ORDER BY timestamp DESC, reversal in app)
```

---

### Module 4: Flow Control & Backpressure Strategy

**FR-4.1 (NATS Queue Config):**
```
MaxPending: 10.000 msgs (= ~40 s buffer @ 250 msg/s nominal [5.000 device × 1 msg/20 s];
                            5 s buffer @ 2.000 msg/s peak bucket)
MaxInflight per Subscriber: 100
Message TTL: None (persist until ACK/NACK)
JetStream retention: 48h / 4 GiB (DiscardOld) — env JETSTREAM_MAX_AGE_HOURS / JETSTREAM_MAX_BYTES
Queue Groups: persistence, live, websocket, alert, media (+ fuel subscribers in alert group)
```

**FR-4.2 (Backpressure Signaling):**
```
Ingestion:    pending > 50% → warn; > 90% → drop telemetry + log error
Persistence:  INSERT latency > 5 s → warn; > 10 s → NACK + requeue (backoff)
WebSocket:    broadcast queue > 10.000 → drop non-critical; tenant-isolated; log every drop
```

**FR-4.3 (Error Recovery):** Retry exponential backoff (1s, 5s, 10s); max 3; all fail →
error queue + log + alert ops. **DO NOT silently drop data without logging.**

**FR-4.4 (Anti Memory-Leak & Bottleneck):** Saat traffic tinggi (≥ 2.000 msg/s,
thousands of WebSocket clients, batch besar), sistem WAJIB:
1. **Pool/goroutine dibatasi:** seluruh worker (NATS, Redis, DB, WS) memakai bounded goroutine
   pool/worker count (bukan unbounded goroutine per message). Tidak ada goroutine yang di-spawn
   tanpa lifecycle (context + done channel).
2. **Buffer bounded:** queue per komponen ber-batas (NATS MaxPending, WS per-conn queue, Redis
   batch, DB batch) — jika penuh, terapkan drop-oldest/backpressure + log (FR-4.2), bukan
   unbounded buffering yang bisa menimbulkan OOM.
3. **Pembatasan request:** `MAX_*` di seluruh entry-point (ingestion max conn, WS max conn,
   HTTP rate limit 100 req/min/user, RPM/limit body size, dsb).
4. **Profil & monitor:** `go_goroutines`, `go_memstats_heap_inuse_bytes`,
   `process_resident_memory_bytes`, `go_gc_duration_seconds` dipantau trend; mendekati batas →
   alert WARNING/CRITICAL (§10.3). Deteksi goroutine leak: `goroutines` tidak kembali ke plateau
   setelah peak.
5. **Context timeout di semua pemanggilan eksternal** (Redis/DB/NATS/HTTP) — tidak ada call
   yang menggantung selamanya.
6. **Load test peak tinggi** (baseline+peak+endurance) wajib menyertakan pemantauan memory
   & goroutine untuk membuktikan tidak ada leak/bottleneck (lihat §16).

---
### Module 5: Real-Time API + WebSocket (service-websocket) + RBAC

**FR-5.1:** Menerima subscribe sesuai hak akses armada (RBAC filter) + tenant isolation.
- Validate JWT → extract `user_id` + `company_code` + `role`; validate master `tm_users` (auth authority).
- Resolve role/permissions via `tm_user_company_access` (role_override, permissions JSON).
  Akses menu/fitur per role di-resolve dari `tm_role_menu_access` (company schema) terhadap
  katalog `tm_menus`/`tm_modules` (master) — v1.7.0; endpoint `GET /api/v1/access/menu` (B12).
- Query `tm_user_vehicles` (company DB) → assigned vehicle IDs; subscribe `vehicle.update.{id}`
  — reject 403 bila company/vehicle tidak diizinkan.

**FR-5.2:** Mengirim pembaruan lokasi via WS setiap data baru (avg 20 s per vehicle sesuai
interval telemetry; server dapat mem-batch/rate-limit jika update datang lebih cepat dari 20 s).
```json
{
  "event": "VEHICLE_UPDATE",
  "data": {
    "imei": "864201040512345", "company_code": "DEV001",
    "plate_number": "B 1234 XYZ", "lat": -6.2088, "lon": 106.8456,
    "speed": 45.2, "heading": 180,
    "acc": true,                       // nilai RIIL tracker (B6 fix), bukan Speed>0
    "status": "MOVING", "battery": 85,
    "fuel_level": 61.5, "fuel_volume": 42.0, "fuel_temp_c": 31.2,  // B5a/B6
    "satellites": 9, "altitude": 112, "gsm_signal": 4,             // B6
    "timestamp": "2026-08-16T10:30:00Z"
  }
}
```

**FR-5.3:** Reconnect otomatis client: server ping 30 s; client backoff (1s, 5s, 10s) + resume.

**FR-5.4 (Resource Limits):** Max conn 5.000+; send buffer 256 KB; max queue/conn 1.000
(drop-oldest + log). Allow empty Origin (non-browser); Origin browser divalidasi.

**FR-5.5 (Company Registration / Auto-Provision + Admin Tenant Otomatis):**
`POST /api/v1/companies` (PLATFORM-only, SuperAdmin) → dalam SATU panggilan transaksional:
(1) auto-create schema `adatrack_gps_{code}` + apply seluruh migration company idempoten (§14.5);
(2) **otomatis membuat 1 akun Admin tenant** untuk company tersebut — email
`admin@{company_code}.local`, **password default `Admin@123`** (bcrypt cost 12, tidak pernah
di-log), role `Admin` + baris `tm_user_company_access`, flag **`must_change_password = true`**;
(3) audit `COMPANY_CREATED` + `TENANT_PROVISIONED` + `ADMIN_USER_AUTOCREATED` (§9.4).
Response `201 Created { code, name, country_code, timezone, database_name, migrations_applied,
admin_user: { email, must_change_password } }`. Idempoten: company yang sudah ada → **tidak**
menimpa password admin (`409 admin_exists`). Detail: **§4.2.1**.
CLI: `migrate-tenant provision -code X -name Y -country ID -tz Asia/Jakarta`.

**FR-5.6 (Users Onboarding, PLATFORM-only):** `POST /api/v1/users` — SuperAdmin membuat akun
primer tenant dalam SATU panggilan: insert `master.users` (bcrypt cost 12) + upsert `tm_user_company_access`
+ opsional `tm_user_vehicles` (transaksional) + audit `USER_CREATED`. Guard: tenant → 403 PLATFORM_ONLY;
role SuperAdmin → 403 PLATFORM_ROLE_RESERVED; konteks `'default'` as code → 400.

**FR-5.7 (Token Lifecycle/B4):** `POST /api/v1/auth/refresh` (refresh token opaque 256-bit,
SHA-256 hash di Redis, rotasi wajib) + `POST /api/v1/auth/logout` (denylist jti ber-TTL) +
cek revocation di requireAuth (401 TOKEN_REVOKED). Env `JWT_REFRESH_EXPIRY_HOURS=168`,
`JWT_REVOCATION_ENABLED=true`. Audit LOGIN_SUCCESS/FAILURE, ACCESS_DENIED, TOKEN_REVOKED.

---

### Module 6: Interactive Web Dashboard (Next.js)

**FR-6.1 (Live Map indicators):** 🟢 Bergerak (Speed > 0, ACC ON) · 🟡 Berhenti (Speed=0, ACC ON)
· ⚫ Offline (>3 min) · 🔴 Alert/Geofence breach.

**FR-6.2:** Real-time list: vehicle status, location, speed.

**FR-6.3 (History Playback):** route 24 jam; kontrol play/pause/stop, speed 1x/2x/5x/10x,
scrubber timeline; query 30 hari < 1,5 s; interpolasi (Bezier) utk smooth animation.

**FR-6.4 (Geofence Management):** CRUD zona circle/polygon + alert rules + mapping vehicle
(lihat §5.9 Feature Specs).

**FR-6.5 (Query Performance):** list vehicles user < 1 s · history 30 d < 1,5 s ·
vehicles-in-geofence < 500 ms.

**FR-6.6 (i18n — lintas-fase):** setup next-intl in F1 (locale `id` default + `en-US`);
pemy empurnaan di F4.1. Semua string UI memakai i18n key.

---
### Module 7: Fuel Sensor Integration (v1.3.0 — B5a)

> Sumber protokol: `docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md`
> ("0D Fuel sensor data", frame contoh CRC `0D12`) + GT06 v1.8.1 (cut-off BBM `DYD`/`HFYD`).

**FR-7.1:** Parse paket fuel sensor GT06 `0x0D` (frame long `79 79`; blok waktu 6 B + ASCII
sensor `!AILOIL,<count>,<fuel>,...`); frame tak-dikenal dicatat (log + counter, no silent-drop).

**FR-7.2:** Teltonika — seluruh AVL IO dikumpulkan generik; mapping IO → fuel via env
(`TELTONIKA_IO_FUEL_LEVEL` default `86`; opsional FUEL_USED, FUEL_TEMP) — support FLS/CAN-bus
sensor tanpa perubahan kode.

**FR-7.3:** Payload standar + field opsional `fuel_level` (%), `fuel_volume` (L), `fuel_temp_c` (°C).
Absen ≠ nol (pointer + omitempty).

**FR-7.4:** Persist → tabel partitioned `th_fuel_logs`; baris berposisi tetap `th_telemetry_logs`;
fuel-only tanpa posisi TIDAK masuk `th_telemetry_logs` (counter).

**FR-7.5:** Live state (worker-live) — merge partial fuel-only ke Redis state; `fuel_level`
ikut live state/API/WS.

**FR-7.6:** Alert **FUEL_DROP** (critical — turun > threshold dalam window) & **REFUEL** (info
— naik > threshold); threshold per-vehicle/global `tm_fuel_configs`; dedup + grace window; publish
`alert.fuel.<company_code>`; notifikasi `fuel_drop`/`refuel` via `tm_notification_preferences`.
> **ACC-gate (keputusan 2026-08-26):** default `FUEL_DROP_REQUIRE_ACC=false` (deteksi selalu
> aktif — anti-siphon saat parkir); strict literal via `=true` + counter `alerts_fuel_acc_suppressed_total`
> + window `FUEL_ACC_STALE_SECONDS` (600s).

**FR-7.7:** REST — `GET /api/v1/vehicles/{id}/fuel/history?from&to` (RBAC row-level) +
CRUD `/api/v1/fuel-configs` (Admin/adatrack Manager).

**FR-7.8:** Kalibrasi voltase→volume out-of-scope core (skema siap tanpa breaking change).

---

### Module 8: Dashcam Event Media (v1.3.0 — Scope A, B5b)

> Keputusan produk 2026-08-23: cakupan **event media** (foto + clip pendek saat
> sos/alarm/geofence/overspeed/manual/scheduled/power) → object storage S3-compatible
> (MinIO dev / S3-OSS prod). **Live streaming video (WebRTC/RTSP) OUT OF SCOPE** fase ini.

**FR-8.1 (Ingest):** `POST /api/v1/media/events` (multipart ATAU JSON + presigned PUT flow)
dengan **HMAC-SHA256 per-company** (header `X-Signature`; secret dari
`master.tm_company_media_config.hmac_secret`).

**FR-8.2 (Storage):** abstraksi `internal/storage` (interface `Store` → `S3Store`/`MemStore`);
key layout `{company}/{vehicle}/{yyyyMM}/{uuid}`; allowlist content-type `image/jpeg`,
`video/mp4`; max ukuran per company (`max_file_mb`).

**FR-8.3 (Katalog):** tabel `th_media_events` (company DB) lifecycle `uploaded → available →
expired | failed`; endpoint `POST /api/v1/media/events/:id/complete` finalisasi JSON flow.

**FR-8.4 (API ber-RBAC):** list/detail, `GET /media/{id}/url` presigned TTL pendek (+audit
`MEDIA_URL_ACCESS`), soft-delete (Admin).

**FR-8.5 (WS):** publish `media.event.<company_code>` → `service-websocket` fan-out
`MEDIA_EVENT` ber-RBAC (pola `notify.alert`).

**FR-8.6 (Capture request):** alert critical/SOS → `media.capture.request.<company_code>`
(GT06 online-command `0x80` utk model yang mendukung; fallback pasif push alarm lokal; jujur).

**FR-8.7 (Retensi):** job harian (`MEDIA_CLEANUP_CRON`, default `0 3 * * *`) hapus objek >
`retention_days` per company + status `expired`.

**FR-8.8 (Metrik/health):** `media_uploads_total{company,type}`, `media_upload_bytes_total`,
`media_presigned_total`, `media_cleanup_deleted_total`, `storage_objects{bucket}`;
`/healthz` = ping object storage + pools + NATS.

**FR-8.9 (Soft Delete Media — §6.0.1):** `DELETE /api/v1/media/{id}` (Admin) = **soft delete** —
status `deleted` + `deleted_at`/`deleted_by`/`delete_reason`, objek fisik di S3 **tidak** dihapus
saat itu (hanya job retensi `expired`, FR-8.7); baris ter-soft-delete disembunyikan dari list/detail
default; restore tersedia via `POST /api/v1/media/{id}/restore` (Admin) + audit `ENTITY_RESTORED`.

---
### 5.9 Feature Specs Konsolidated (dari FEATURE_COMPLETENESS + GAPS)

#### 5.9.1 GEOFENCE (B3)
- **Definisi zona:** `area_type ENUM('circle','polygon')`; circle → `radius_meters`;
  polygon → `boundary_points`; `coordinates` GeoJSON.
- **Mapping vehicle:** tabel `tm_geofence_vehicles` (many-to-many, `enabled` boolean).
- **Deteksi real-time:** worker-alert subscribes `telemetry.raw.>`; circle = Haversine vs
  center; polygon = ray-casting; state entry/exit di Redis
  `{prefix}{company}:geofence_state:{imei}`; state change → alert + publish
  `alert.geofence.<company>` (entry & exit berturut-turut terdeteksi).
- **Rules:** entry & exit trigger; severity low/medium/high/critical; per-user rules via
  tm_notification_preferences. Multi-zone logic: vehicle can be in 2+ zones → alert per zone.

#### 5.9.2 ROUTE / NAVIGATION (B3)
- **Route:** waypoints + est. time; status `not_started → in_progress → completed | delayed`.
- **Assignment:** `th_route_assignments` ⋈ `tm_routes` ⋈ `tm_vehicles` (driver+vehicle).
- **Deviation:** distance to nearest waypoint > `ROUTE_DEVIATION_THRESHOLD_M` (default 200 m)
  → alert `ROUTE_DEVIATION` high + `deviation_meters` (max) ter-update; refresh 30 s.
- **API (api-vehicle):** create/detail/list/update/soft-delete route, assign/unassign,
  manual status transition `PATCH /routes/:id/assignments/:aid` (RBAC Admin/Manager).

#### 5.9.3 HISTORY PLAYBACK (F3 ⬜ backend ready)
- Playback kontrol: play/pause/stop, speed 1x/2x/5x/10x, seek(timestamp), getPlaybackState.
- Interpolasi smooth (Bezier) antara titik; query 30 d < 1,5 s.
- Export: KML/GPX (future). Point reduction (RDP) → B7.4 ⬜.

#### 5.9.4 MAX SPEED / OVERSPEEDING (B3)
- **Config:** `tm_speed_configs` per-vehicle (vehicle_id NOT NULL = specific, NULL = global) +
  `grace_margin_percent` per baris; severity `critical` bila > 1,5× limit efektif.
- **Deteksi:** worker-alert tiap msg; config efektif (vehicle-specific wins); alert
  `OVERSPEEDING` + publish `alert.speed.<company>`; dedup window; notification per pref.
- **Escalation:** repeat_alert_interval (dedup window) — persistent while speeding.

#### 5.9.5 SOS / EMERGENCY (B3)
- Trigger: GT06 alarm `0x26`/`0x27`/`0x19` SOS code; severity **critical**.
- Life-cycle: open → acknowledged → resolved (ack via `PATCH /alerts/{id}/acknowledge`).
- **Eskalasi otomatis:** tiap 30 s utk SOS open > `SOS_ESCALATION_MINUTES` (counter Redis,
  cap `SOS_ESCALATION_MAX`); **TTA** dicatat sekali per alert
  (`sos_time_to_acknowledge_seconds`).
- Notification: websocket fan-out `notify.alert.<vehicle_id>` + email/SMS per prefs.

#### 5.9.6 BATTERY LOW (B3)
- Threshold < 20% (default) → alert `BATTERY_LOW`; dedup window + guard open-alert.
- Config future: `battery_configs` (low/critical levels) — out-of-scope core.

#### 5.9.7 OFFLINE (B3)
- Live-state Redis key hilang/stale > `OFFLINE_AFTER_MINUTES` (default 3 min) → alert
  `OFFLINE` (dedup satu open alert per vehicle); status OK → resolve.

#### 5.9.8 NOTIFICATION DELIVERY (B3/B5a)
- Resolusi penerima: user berhak vehicle (`tm_user_vehicles`) ∪ Admin company.
- Filter per `tm_notification_preferences` (per user/type/channel/min_severity, `enabled`).
- Channel **websocket**: publish `notify.alert.<vehicle_id>` (fan-out RBAC service-websocket).
- Channel **email/SMS/push**: audit row `td_notifications` status `pending → sent/delivered →
  failed → skipped` + reason (no silent drop). Retry backoff + dead-letter NATS.
- Template per alert type: `backend/services/worker-alert/templates/{type}_{channel}.tmpl`.
- Rate limit `NOTIFICATION_RATE_LIMIT` per company per detik.

#### 5.9.9 ANALYTICS & REPORTING (menunggu F4)
- Trip summary (distance/duration/avg speed/stops — data from B7.2 `th_vehicle_trips`),
  violation summary, driver scorecard, export PDF/Excel, scheduled reports. ⬜ planned.

---
### 5.10 Module 9: Enterprise & Industry Modules — Acuan `docs/FRONTEND.md` (v1.7.0, ⬜ B12)

> **Sumber:** `docs/FRONTEND.md` — struktur modul/menu/fitur aplikasi Frontend
> **Business** (§1) & **Personal** (§2). **Penerapan backend mengikuti struktur tsb:**
> setiap menu Frontend wajib punya dukungan data/API backend (tabel §6 + endpoint §8).
> Core tracking (menu Utama & Master Data inti) sudah tercakup B0–B7; modul lain di
> bawah **⬜ planned (B12)** kecuali ditandai lain. Semua tabel baru memakai prefix
> `tm_`/`th_`/`td_` (§6.0), RBAC row-level (§3.1), soft delete (§6.0.1), audit (§9.4).

**FR-9.1 (Pemetaan Menu → Backend — Business, FRONTEND.md §1):**

| Menu (FRONTEND.md §1) | Dukungan Backend | Status |
|---|---|---|
| 1.1 Utama — Beranda / Pemantauan (Live Map, Heatmap, Playback) / Perjalanan (Trips) | live state + history + trips (B7.2); Heatmap = agregasi historis | ⬜ B0–B7 (Heatmap ⬜ B12) |
| 1.2 Master — Armada (Vehicles) · Geofences · Routes | CRUD + row-level RBAC (§8.2) | ⬜ B2/B3 |
| 1.2 Master — **Pengemudi (Drivers)** · **Grup (Groups)** | master data pengemudi & pengelompokan vehicle/driver + mapping | ⬜ B12 |
| 1.3 Akses — **Personel** · **Kartu (Card/RFID)** · **Log** (riwayat akses) | identitas personel, kartu akses/RFID, riwayat akses kontrol | ⬜ B12 |
| 1.4 Aset — **Assets** · **Maintenance** | registry aset; jadwal servis + reminder odometer/engine-hours | ✅ B12 (maintenance menyambung B8) |
| 1.5 Keamanan — **Safety** (skor mengemudi) · **Incidents** | skor mengemudi (driver behavior B8) + katalog insiden/pelanggaran | ✅ B8/B12 |
| 1.6 Analisis — **Reports** · **Analytics** | laporan terjadwal & dashboard tren (trip/violation summary, export PDF/Excel) | ⬜ F4/B12 |
| 1.7 Industry Specific — Rental · Transport · Logistics · Sales · Field Service · Patrol · Project Site | modul vertikal per model bisnis (per-tenant, flag lisensi di `tm_companies`) | ⬜ B12+ (bertahap) |
| 1.8 Administrasi — Users Access (RBAC) · GPS Devices | users/RBAC (B2) · inventaris device (`tm_vehicle_imei_map`) | ⬜ parsial (B2/B3) |
| 1.8 Administrasi — **Organization** · **Integrations** (API/Webhook) · **Settings** | struktur hierarki organisasi; webhook outbound + API key pihak ketiga; preferensi tenant | ⬜ B12 |

**FR-9.2 (Aplikasi Personal/B2C — FRONTEND.md §2):** menu Tracking/Statistics/Settings
memakai pipeline yang sama (telemetry/live/alert) dengan auth `tm_users_b2c` + scope
ringkas (§4.2); Statistics = agregasi usage; Settings = bahasa/tema/notifikasi dasar.
Tidak mengubah pipeline B2B (non-blocking).

**FR-9.3 (Fitur lintas aplikasi — FRONTEND.md §3):** i18n id/en-US (F4.1), theming
(Frontend), Maps Foundation (F1), **Notifikasi & Berbagi Lokasi** — share lokasi sementara
ke publik → link token ber-TTL + endpoint publik read-only (`GET /api/v1/share/{token}`)
tanpa auth, ter-audit, revoke-able ⬜ B12.

**FR-9.4 (Kontrak umum modul B12):** setiap modul wajib mengikuti pola existing —
response format & error_code §8.1, pagination, RBAC §3.1, audit §9.4, soft delete +
restore §6.0.1/§8.2, input validation §8.5; endpoint prefix `/api/v1/{module}`
(drivers, groups, personel, cards, access-logs, assets, maintenance, incidents,
organizations, integrations, share, …); tabel `tm_`/`th_`/`td_` padanannya didefinisikan
saat B12 dimulai (§6 ditambah additive, tanpa breaking change). Katalog module/menu
hidup di **master** (`tm_modules`/`tm_menus`, seed dari `docs/FRONTEND.md` §1–§3); akses
menu per role hidup **di company schema per-tenant — bukan master** (`tm_role_menu_access`,
§6.2), di-seed default saat provisioning.

---
## 6. Database Schema

> **Arsitektur:** schema-per-tenant (PostgreSQL) — satu database fisik, schema per tenant
> (`adatrack_gps_master`, `adatrack_gps_{code}`). Master schema untuk data global/reference +
> otentikasi; company schema per tenant untuk data operasional.
> Migrations source: `backend/database/migrations/{master_pg,company_pg}/` (PostgreSQL).
> Auto-provision tenant applies semua migration company.

### 6.0 Konvensi Penamaan Tabel (NORMALIZED — tm_ / th_ / td_)

Seluruh tabel memakai **prefix 3-huruf** berdasarkan tipe data (normalisasi penamaan, B10):

| Prefix | Kategori | Contoh |
|---|---|---|
| **`tm_`** | **Master** — reference, registry, config (statis/relatif jarang berubah) | `tm_vehicles`, `tm_geofences`, `tm_speed_configs` |
| **`th_`** | **Transaksi Header** — satu record "kepala" per kejadian transaksi | `th_telemetry_logs`, `th_alerts`, `th_vehicle_trips` |
| **`td_`** | **Transaksi Detail** — baris rincian anak dari header (FK ke `th_*`) | `td_vehicle_stops`, `td_notifications` |

> **Catatan migrasi:** migrasi lama masih memakai nama tanpa prefix (mis. `tm_users`, `tm_vehicles`,
> `th_telemetry_logs`). Normalisasi di-apply via migrasi rename idempoten pada **fase B10**
> tanpa mengubah kolom/FK. Seluruh kode/query baru WAJIB memakai nama ternormalisasi.

**Mapping penamaan (canonical → legacy):**

| Canonical (baru) | Legacy (lama) | Jenis |
|---|---|---|
| `tm_companies` | `companies` | Master |
| `tm_countries` · `tm_provinces` · `tm_cities` · `tm_districts` · `tm_subdistricts` | `countries` · `provinces` · `cities` · `districts` · `subdistricts` | Master |
| `tm_users` | `users` | Master (B2B) |
| `tm_users_b2c` | — (baru) | Master (B2C) |
| `tm_user_company_access` | `user_company_access` | Master |
| `tm_modules` · `tm_menus` | — (baru, v1.7.0) | Master (registry modul & menu, acuan FRONTEND.md) |
| `tm_role_menu_access` | — (baru, v1.7.0) | Company per-tenant (role → akses menu) |
| `tm_vehicles` | `vehicles` | Master |
| `tm_user_vehicles` | `user_vehicles` | Master (junction RBAC) |
| `tm_vehicle_imei_map` | `vehicle_imei_map` | Master |
| `tm_vehicle_categories` · `tm_vehicle_types` | `vehicle_categories` · `vehicle_types` | Master |
| `tm_geofences` · `tm_geofence_vehicles` | `geofences` · `geofence_vehicles` | Master |
| `tm_speed_configs` · `tm_fuel_configs` | `speed_configs` · `fuel_configs` | Master |
| `tm_notification_preferences` · `tm_routes` | `notification_preferences` · `routes` | Master |
| `tm_company_media_config` · `tm_audit_logs` | `company_media_config` · `audit_logs` | Master |
| `th_telemetry_logs` | `telemetry_logs` | Transaksi Header (partitioned) |
| `th_fuel_logs` | `fuel_logs` | Transaksi Header (partitioned) |
| `th_alerts` | `alerts` | Transaksi Header |
| `th_route_assignments` | `route_assignments` | Transaksi Header |
| `th_media_events` | `media_events` | Transaksi Header |
| `th_vehicle_trips` | `vehicle_trips` | Transaksi Header |
| `td_vehicle_stops` | `vehicle_stops` | Transaksi Detail (FK → th_vehicle_trips) |
| `td_notifications` | `notifications` | Transaksi Detail (FK → th_alerts) |

---

### 6.0.1 Kebijakan Penghapusan Data — Soft Delete (WAJIB)

**Aturan utama: seluruh penghapusan data bisnis dilakukan secara _soft delete_; `DELETE` fisik
dilarang dari seluruh handler REST/WS.**

| Aspek | Ketentuan |
|---|---|
| **Kolom wajib** | Setiap tabel master (`tm_`) dan transaksi header (`th_`) memiliki `deleted_at TIMESTAMPTZ NULL`, `deleted_by BIGINT NULL`, `delete_reason VARCHAR(255) NULL`. Tabel detail (`td_`) ikut ter-soft-delete bersama parent-nya. |
| **Query default** | Semua query baca menambahkan `WHERE deleted_at IS NULL` (default scope). Data terhapus hanya terlihat dengan `?include_deleted=true` — khusus SuperAdmin/Admin dan **selalu ter-audit** (`SOFT_DELETED_VIEWED`). |
| **Uniqueness** | Constraint unik memakai **partial unique index** (`UNIQUE ... WHERE deleted_at IS NULL`) agar nilai (mis. `imei`, `plate_number`, `email`) dapat dipakai kembali setelah record lama di-soft-delete. |
| **Cascade** | Penghapusan parent (company/vehicle/route) men-soft-delete anak terkait dalam **satu transaksi** + satu baris audit berisi daftar entitas terdampak. Tidak ada `ON DELETE CASCADE` yang menghapus riwayat. |
| **Riwayat telemetry/alert** | `th_telemetry_logs`, `th_fuel_logs`, `th_alerts`, `th_media_events` **tidak** di-soft-delete per baris — siklus hidupnya mengikuti retensi partisi/job (§11), bukan aksi user. |
| **Restore** | Endpoint `POST /api/v1/{resource}/{id}/restore` (Admin; role lain → `403`) mengembalikan `deleted_at=NULL` + audit `ENTITY_RESTORED` (alasan wajib). |
| **Media** | Soft delete = status `deleted` + `deleted_at`; objek fisik dihapus hanya oleh job retensi (`expired`, FR-8.7). |
| **Hard delete** | Hanya oleh **job retensi/archival** (§11) atau permintaan **GDPR right-to-be-forgotten** (§15) — keduanya wajib menulis audit `HARD_DELETE` (alasan, jumlah baris, aktor sistem). |
| **Efek ke pipeline** | Vehicle/geofence/route ter-soft-delete **dikeluarkan** dari deteksi alert, fan-out WebSocket, dan evaluasi geofence (worker-live/worker-alert memfilter `deleted_at IS NULL`). IMEI yang di-soft-delete tidak lagi lolos anti-spoofing. |
| **Verifikasi** | Wajib ada unit/integration test: delete → row masih ada + `deleted_at` terisi; list default tidak memuat; `include_deleted=true` memuat; restore berhasil; audit tercatat (§16). |

---

### 6.1 Master Database (`adatrack_gps_master`)

Tabel (migrasi master 001–013 + registry module/menu v1.7.0; normalisasi `tm_` B10):

- **`tm_companies`** — tenant registry **tipe bisnis**: code, name, legal_name, tax_id,
  country_code FK → `tm_countries`, timezone, address, phone, **`business_type
  ENUM('b2b','b2c') DEFAULT 'b2b'`** (multi-tenant = B2B), created_by/updated_by, deleted_at.
- **`tm_countries` / `tm_provinces` / `tm_cities` / `tm_districts` / `tm_subdistricts`** —
  reference wilayah (ISO 3166-1, 38 provinsi IDN, 514 kab/kota, 7.285 kecamatan, 83.762 desa — seed real).
- **`tm_users`** (B2B) — **auth authority tipe bisnis B2B (multi-tenant)**: email+pwd_hash
  (bcrypt cost 12), global_role (`SuperAdmin|Admin|adatrack_MANAGER|OPERATOR|DRIVER`),
  is_active, **`must_change_password BOOLEAN`**, `password_changed_at`, last_login_at,
  `deleted_at/deleted_by` (soft delete §6.0.1). Akun admin tenant pertama dibuat **otomatis**
  saat company dibuat (FR-5.5) dengan password default `Admin@123` + `must_change_password=true`.
- **`tm_users_b2c`** (B2C) — **auth authority tipe bisnis B2C (single-tenant konsumen)**:
  email+pwd_hash (bcrypt cost 12), role terbatas `USER|OWNER` (end-user/device owner),
  phone, is_active, `must_change_password`, last_login_at, **tanpa akses ke tenant B2B**
  (isolasi penuh). Tabel ini mengisi kebutuhan "user dibagi 2 berdasarkan tipe bisnis" di master DB.
- **`tm_modules`** — **registry modul aplikasi (v1.7.0, acuan `docs/FRONTEND.md`)**: code UNIQUE,
  name, `app ENUM('business','personal')`, sort_order, enabled; katalog global di-seed idempoten
  dari FRONTEND.md §1–§3 — Business: utama, master-data, akses, aset-perawatan, keamanan,
  analisis-laporan, industry-specific, administrasi; Personal: tracking, statistics, settings;
  modul baru (B12) cukup INSERT additive.
- **`tm_menus`** — **registry menu per modul**: module_id FK → `tm_modules`, code UNIQUE
  (mis. `business.tracking.live_map`), name, path (route frontend), parent_id (submenu),
  sort_order, enabled; seed mengikuti struktur menu FRONTEND.md; dipakai untuk render
  navigasi dinamis + resolve akses per role (§6.2 `tm_role_menu_access`).
- **`tm_vehicle_imei_map`** — lookup IMEI → company_code (anti-spoofing/tenant resolution).
- **`tm_vehicle_categories` / `tm_vehicle_types`** — master referensi (PVB/LCV/HCV/TW/THW/EV/SPV;
  type per category).
- **`tm_audit_logs`** — **audit trail wajib (§9.4)**, append-only: `action`, `outcome`,
  `actor_user_id/email/role`, `company_code`, `entity_type`, `entity_id`, `before`/`after`
  (JSON ter-redaksi), `ip_address`, `user_agent`, `request_id`, `reason`, `created_at`.
  Mencatat LOGIN_SUCCESS/FAILURE, ACCESS_DENIED, TOKEN_REVOKED, USER_CREATED,
  COMPANY_CREATED/TENANT_PROVISIONED, mutasi data (create/update/**soft delete**/restore),
  perubahan konfigurasi, IMEI register/reject, MEDIA_URL_ACCESS, MIGRATION_APPLIED, HARD_DELETE.
  Tanpa `UPDATE`/`DELETE`; kegagalan tulis → retry + dead-letter + metrik (`no silent drop`).
- **`tm_schema_migrations`** — ledger migrasi otomatis (§14.5): `version`, `checksum`,
  `applied_at`, `success`, `duration_ms`, `applied_by` (master & per schema tenant).
- **`tm_company_media_config`** — bucket, retention_days, max_file_mb, hmac_secret per company.
- **platform tenant** — company registry `'DEFAULT'` → `adatrack_gps_default` = konteks platform.

### 6.2 Company Database (`adatrack_gps_{LOWER(company_code)}`)

Tabel (migrasi company 001–017; normalisasi `tm_`/`th_`/`td_` B10):

> **Soft delete (§6.0.1):** seluruh tabel master (`tm_`) dan transaksi header (`th_`) memiliki
> `deleted_at`/`deleted_by`/`delete_reason`; query default memfilter `deleted_at IS NULL`.
> Tabel ber-riwayat tinggi (`th_telemetry_logs`, `th_fuel_logs`, `th_media_events`) mengikuti
> retensi partisi (§11), bukan aksi user.

**Master (`tm_`):**
- **`tm_user_company_access`** — registry user lokal (user_id ref `master.tm_users.id` (B2B) —
  NO cross-DB FK; role_override, is_active, permissions JSON).
- **`tm_role_menu_access`** — **akses menu per role (v1.7.0 — di company schema, BUKAN master)**:
  role (global atau `role_override` dari `tm_user_company_access`), menu_id ref **logis**
  `master.tm_menus.id` (tanpa hard FK — nama schema dinamis; integritas dijaga service layer +
  seed idempoten), `can_view/can_create/can_edit/can_delete`, enabled, soft delete (§6.0.1);
  seed default per-role saat provisioning tenant (init-pg `03_company_setup`): Admin = semua
  menu, role lain subset. Frontend merender navigasi dinamis via `GET /api/v1/access/menu`
  (resolusi: user → role efektif → `tm_role_menu_access` → menu enabled). Admin CRUD akses
  per role (§8.2, B12).
- **`tm_vehicles`** — enterprise schema: identity (imei UNIQUE, plate_number, make/model/variant,
  year, engine/chassis/VIN, color, fuel_type ENUM), classification (category/type code),
  compliance (registration/insurance/road_tax/inspection expiry), physical specs (GVW,
  payload, dims mm), driver (driver_user_id, device_model, firmware_version), live state
  denorm (last_seen_at, current_lat/lon/speed), **odometer_km + engine_hours** (016, B7.1),
  soft-delete (deleted_at), status ENUM active/inactive/maintenance. Index: imei, status,
  type/category, driver, last_seen, chassis UNIQUE, current_location.
- **`tm_user_vehicles`** — RBAC junction (user_id, vehicle_id UNIQUE; FK `ON DELETE CASCADE`
  hanya berlaku saat **hard delete** job retensi — operasi normal memakai soft delete).
- **`tm_geofences`** — name, area_type circle/polygon, coordinates GeoJSON, radius_meters,
  boundary_points, created_by.
- **`tm_geofence_vehicles`** — mapping geofence ↔ vehicle (enabled).
- **`tm_speed_configs`** — max_speed_kmh, grace_margin_percent, alert_severity, enabled; global/per-vehicle.
- **`tm_fuel_configs`** — threshold drop/refuel per-vehicle (vehicle_id NULL = default global).
- **`tm_notification_preferences`** — per user/type/channel/enabled/min_severity.
- **`tm_routes`** — waypoints, driver/vehicle, status, deviation.

**Transaksi Header (`th_`):**
- **`th_telemetry_logs`** — partitioned monthly: vehicle_id, imei, company_code, lat/lon DECIMAL,
  speed/heading/altitude FLOAT, acc_status SMALLINT (0/1), battery_level, timestamp, created_at;
  PK (id, timestamp); index (vehicle_id, timestamp DESC), (imei, timestamp DESC); spatial index.
- **`th_fuel_logs`** — partitioned monthly; fuel readings (fuel_level/volume/temp_c, posisi).
- **`th_alerts`** — type ENUM lowercase (geofence_breach/overspeeding/battery_low/offline/sos/
  route_deviation/fuel_drop/refuel), severity low/medium/high/critical, vehicle_id, lat/lon,
  metadata JSON, status open/acknowledged/resolved, acknowledged_by, resolved_at.
- **`th_route_assignments`** — assignment rute (route_id, vehicle_id, driver_user_id, status).
- **`th_media_events`** — dashcam media catalog: vehicle_id, status uploaded/available/
  expired/failed, storage_key, content_type, media_type, size_bytes, uploaded_at, expires_at.
- **`th_vehicle_trips`** — segmentasi perjalanan (start/end time, lat/lon, distance_km,
  max/avg speed, stop_count, duration_seconds).

**Transaksi Detail (`td_`):**
- **`td_vehicle_stops`** — detail stop milik trip (FK → `th_vehicle_trips.id`): start/end time,
  duration_seconds, lat/lon (017, B7.2).
- **`td_notifications`** — audit pengiriman per alert (FK → `th_alerts.id`): alert_id, user_id,
  channel, status, provider_response JSON, error reason.

---
## 7. Configuration Management

> **Dua set konfigurasi (B10):** seluruh compose dan environment dibuat dalam **dua
> varian terpisah** — (1) **LOCAL** untuk development, (2) **COOLIFY** untuk production deploy:
>
> | Varian | File Compose | File Env | Pemakaian |
> |---|---|---|---|
> | **LOCAL** | `backend/docker-compose.yml` (canonical) + `backend/deployments/docker-compose.local.yml` | `backend/.env` (template `.env.example`) | Dev/staging mesin lokal |
> | **COOLIFY** | `backend/deployments/docker-compose.coolify.yml` | `backend/.env.coolify` | Production di Coolify (resource nativ, proxy, TLS) |
>
> - Kedua varian memakai service/config yang sama (PostgreSQL, Redis, NATS, MinIO), hanya
>   berbeda pada bind-port, credentials, dan integrasi proxy Coolify.
> - **`compose-up.sh`** membaca provider & memilih varian (`local`/`coolify`) otomatis;
>   tidak ada config yang tercampur antar environment.
> - Nilai di `.env.coolify` **tidak boleh** berisi secret dev; secret produksi dikelola via
>   resource Coolify. Detail deploy: §14 + `docs/DEPLOY_COOLIFY.md` + `PANDUAN_BACKEND.md`.

---

### 7.1 Database Engine (PostgreSQL)

```dotenv
POSTGRES_HOST=postgres  POSTGRES_PORT=5432  POSTGRES_DB=adatrack_gps_master
POSTGRES_USER=adatrack  POSTGRES_PASSWORD=...
DATABASE_URL=            # opsional; prioritas tertinggi
```

| Aspek | `postgres` |
|---|---|
| Driver | `pgx` v5 stdlib (via `database/sql`); normalisasi placeholder `?`→`$N` di `internal/dialect` |
| Koneksi | `POSTGRES_*` (atau `DATABASE_URL`, prioritas tertinggi; `search_path` di-set paksa) |
| Multi-tenant | 1 DB fisik + **schema** per tenant (`adatrack_gps_master`, `adatrack_gps_{code}`) |
| Migrasi | `database/migrations/{master_pg,company_pg}/` — versioned + ledger `tm_schema_migrations`; di-apply **otomatis** saat deploy Coolify & auto-provision tenant (§14.5) |
| Bootstrap | `database/init-pg/` (incl. seed reference idempoten — `ON CONFLICT DO UPDATE`) |
| Pool sizing | `POSTGRES_POOL_MIN/MAX` |

- Akses DB terpusat di paket **`internal/dialect`**: quoting identifier, upsert
  `ON CONFLICT DO UPDATE`, `RETURNING id`, pemisahan statement multi-baris, normalisasi placeholder.

### 7.2 Environment Variables (daftar acuan: `backend/.env` + `.env.example`)

**Infra & shared:** `COMPOSE_VARIANT=local|coolify`
(pemilih varian config §7/§14), `TELEMETRY_INTERVAL_SECONDS=20`, `NATS_URL`,
`NATS_SUBJECT_PREFIX`, `MASTER_DB_*`, `COMPANY_DB_PREFIX`, `REDIS_HOST/PORT`, `JWT_SECRET`,
`LOG_LEVEL`.

**ingestion-tcp:** `TCP_PORT=9003` (dev override; kanonik proyek 9000, konvensi Traccar 5001),
`TCP_MAX_CONNECTIONS=5000`, `TELTONIKA_TCP_PORT=9011` (referensi sendiri), `TK103_TCP_PORT=9002`,
`GT06_DATE_BCD=false` (plain-hex default).
**Protokol universal (Module 1c — port per konvensi Traccar, `0` = nonaktif):**
`PORT_MEILIGAO_TCP=5002`, `PORT_XEXUN_TCP=5003`, `PORT_SUNTECH_TCP=5017`, `H02_TCP_PORT=5010`,
`PORT_TOTEM_TCP=5005`, `PORT_GT02_TCP=5006`, `PORT_NAVIGIL_TCP=5012`, `PORT_CASTEL_TCP=5019`
(+ `<PROTOCOL>_TCP_PORT` untuk protokol berikutnya) — boot menolak port bentrok (`os.Exit(1)`).

**worker-persistence:** `BATCH_SIZE=500`, `BATCH_TIMEOUT_SEC=5`, `RETRY_MAX=3`,
`RETRY_BACKOFF_MS=1000,5000,10000`, `POSTGRES_POOL_MIN=20`, `POSTGRES_POOL_MAX=50`.

**worker-live:** `REDIS_KEY_PREFIX=adatrack_gps:` (format `adatrack_gps:{company}:vehicle:state:<IMEI>`),
`REDIS_POOL_MIN=10`, `REDIS_POOL_MAX=30`, `REDIS_TTL_SEC=300`, `BATCH_UPDATE_INTERVAL_MS=100`.

**service-websocket / api-vehicle:** `HTTP_PORT=8080/8081`, `JWT_EXPIRY_HOURS=24`,
`WS_MAX_CONNECTIONS=5000`, `WS_SEND_BUFFER_KB=256`, `WS_MAX_QUEUE=1000`,
`COMPANY_MIGRATIONS_DIR=./backend/database/migrations/company_pg`.

**worker-alert:** `OFFLINE_AFTER_MINUTES=3`, `ROUTE_DEVIATION_THRESHOLD_M=200`,
`SOS_ESCALATION_MINUTES`, `SOS_ESCALATION_MAX`, `SOS_COOLDOWN_SECONDS`, + SMTP/SMS
(§7.3) + fuel env (§7.4) + media env (§7.5).

### 7.3 Notification Delivery (Email + SMS) — env
```dotenv
SMTP_HOST=smtp.gmail.com  SMTP_PORT=587  SMTP_USERNAME=...  SMTP_PASSWORD=...
SMTP_FROM=notifications@adatrackgps.io  SMTP_TLS=true
SMS_PROVIDER=none|twilio|aws_sns  SMS_TWILIO_ACCOUNT_SID=...  SMS_AUTH_TOKEN=...  SMS_FROM=...
NOTIFICATION_RETRY_MAX=3  NOTIFICATION_RETRY_BACKOFF_MS=2000,6000,12000
NOTIFICATION_BATCH_INTERVAL_MS=100  NOTIFICATION_RATE_LIMIT=100
```

### 7.4 Fuel Sensor — env
```dotenv
TELTONIKA_IO_FUEL_LEVEL=86  TELTONIKA_IO_FUEL_USED=  TELTONIKA_IO_FUEL_TEMP=
FUEL_DROP_THRESHOLD_PERCENT=15  FUEL_WINDOW_SECONDS=300  FUEL_REFUEL_THRESHOLD_PERCENT=10
FUEL_DROP_REQUIRE_ACC=false  FUEL_ACC_STALE_SECONDS=600
```

### 7.5 Dashcam Media — env
```dotenv
SERVICE_MEDIA_HTTP_ADDR=:8095
MEDIA_S3_ENDPOINT=http://localhost:9000  MEDIA_S3_BUCKET=adatrack-media
MEDIA_S3_ACCESS_KEY=minioadmin  MEDIA_S3_SECRET_KEY=minioadmin  MEDIA_S3_USE_SSL=false
MEDIA_PRESIGN_TTL_SECONDS=600  MEDIA_MAX_FILE_MB=100  MEDIA_CLEANUP_CRON=0 3 * * *
```

### 7.6 JetStream Retention
```dotenv
JETSTREAM_MAX_AGE_HOURS=48   # default
JETSTREAM_MAX_BYTES=4294967296  # 4 GiB; DiscardOld
```

### 7.7 Read Replica / HA — env
```dotenv
DB_REPLICA_HOST/PORT/USER/PASSWORD  DB_REPLICA_PROBE_SECONDS=5
REDIS_REPLICA_HOST=...  (per HA overlay deployments/docker-compose.ha.yml)
```

### 7.8 Migrasi Otomatis, Audit Trail & Soft Delete — env
```dotenv
# Migrasi otomatis (§14.5) — Coolify pre-deploy + boot service
MIGRATE_ON_BOOT=true                 # service meng-apply migrasi saat start
MIGRATE_LOCK_TIMEOUT_SEC=60          # PostgreSQL advisory lock (deploy konkuren)
MIGRATION_LEDGER_TABLE=tm_schema_migrations
COOLIFY_PREDEPLOY_MIGRATE=true       # menjalankan scripts/migrate.sh sebelum deploy
# Audit trail wajib (§9.4)
AUDIT_ENABLED=true
AUDIT_BUFFER_SIZE=10000              # bounded (anti memory-leak FR-4.4)
AUDIT_FLUSH_INTERVAL_MS=500
AUDIT_RETENTION_DAYS=365
AUDIT_FAIL_CLOSED=true               # aksi sensitif gagal-audit → request ditolak
SOFT_DELETE_RETENTION_DAYS=180       # purge fisik baris deleted_at (§11)
PASSWORD_DEFAULT_TENANT_ADMIN=Admin@123   # FR-5.5 — hanya berlaku sampai login pertama
```

---
## 8. API & WebSocket Contracts

### 8.1 REST API Response Format (GAP #1/#3 — seragam)

**Success:**
```json
{
  "status": "success",
  "data": { ... },
  "pagination": { "page": 1, "limit": 100, "total": 5000 }   // opsional
}
```

**Error:**
```json
{
  "status": "error",
  "error_code": "VEHICLE_NOT_FOUND",
  "message": "Vehicle with ID 999 not found",
  "timestamp": "2026-08-16T10:30:00Z"
}
```

**HTTP Status Codes:**
- `200 OK` · `201 Created` · `400 Bad Request` (invalid query/body) · `401 Unauthorized`
  (missing/invalid JWT; `401 TOKEN_REVOKED` bila token di-denylist) · `403 Forbidden`
  (RBAC/cross-tenant; `403 PLATFORM_ONLY`, `403 PLATFORM_SCOPE`, `403 PLATFORM_ROLE_RESERVED`)
  · `404 Not Found` · `429 Too Many Requests` (rate limit) · `500 Internal Server Error`
  · `503 Service Unavailable` (DB/Redis down — graceful degradation).

### 8.2 REST Endpoints (canonical)

| Method | Path | Service | RBAC |
|---|---|---|---|
| POST | `/api/v1/auth/login` | websocket+api-vehicle | public (rate-limit 5/15m) |
| POST | `/api/v1/auth/refresh` · `/api/v1/auth/logout` | beide | JWT |
| POST | `/api/v1/companies` · `/api/v1/users` | websocket | **PLATFORM-only** |
| GET | `/api/v1/vehicles` · `/api/v1/vehicles/{id}` | websocket/api-vehicle | JWT + row-level |
| GET | `/api/v1/vehicles/{id}/history` | websocket | JWT + row-level |
| GET | `/api/v1/vehicles/{id}/fuel/history?from&to` | api-vehicle | JWT + row-level |
| POST/PUT/DELETE | `/api/v1/vehicles[/{id}]` | api-vehicle | Admin/Manager |
| POST/DELETE | `/api/v1/vehicles/{id}/users` | api-vehicle | Admin/Manager |
| GET/POST | `/api/v1/geofences` · `/api/v1/geofences/{id}` | websocket | JWT (write: Admin) |
| GET/POST | `/api/v1/geofence-vehicles` (list/add/remove) | api-vehicle | Admin/Manager |
| GET/PATCH | `/api/v1/alerts` · `/api/v1/alerts/{id}/acknowledge` | websocket | JWT + row-level |
| GET/POST | `/api/v1/routes` · `GET/PATCH/DELETE /routes/{id}` · `GET /routes/{id}/track` | websocket | Admin/Manager |
| PATCH | `/api/v1/routes/{id}/assignments/{aid}` | api-vehicle | Admin/Manager |
| CRUD | `/api/v1/speed-configs` | api-vehicle | Admin/Manager |
| CRUD | `/api/v1/fuel-configs` | api-vehicle | Admin/adatrack Manager |
| POST | `/api/v1/media/events` (multipart/JSON+presigned) | service-media | **HMAC X-Signature** |
| POST | `/api/v1/media/events/{id}/complete` | service-media | HMAC |
| GET | `/api/v1/media` · `/media/{id}` · `/media/{id}/url` | service-media | JWT + row-level (+audit) |
| DELETE | `/api/v1/media/{id}` | service-media | Admin (soft delete — FR-8.9) |
| POST | `/api/v1/{resource}/{id}/restore` | websocket / api-vehicle | **Admin** — restore soft-deleted (§6.0.1) |
| GET | `/healthz` · `/metrics` | semua service | ops |

> **Catatan soft delete (§6.0.1):** seluruh endpoint `DELETE` = **soft delete**
> (`deleted_at` terisi) — **bukan** `DELETE` fisik. Endpoint `PATCH`/`DELETE` long-form
> tersedia untuk vehicles, geofences, routes, speed-configs, fuel-configs, user-vehicle
> assignments, dan users. Setiap `DELETE`/restore **wajib** menulis audit (§9.4).

> **Endpoint modul `docs/FRONTEND.md` (v1.7.0):** tabel di atas = kontrak stabil (B2–B5b).
> Menu lain di FRONTEND.md (drivers, groups, personel, kartu RFID, log akses, assets,
> maintenance, safety/insiden, laporan/analitik lanjutan, organization, integrations,
> share lokasi publik, modul industry-specific) didefinisikan pada fase **B12** mengikuti
> §5.10 (Module 9) — **additive-only**, tidak mengubah endpoint existing.

### 8.3 WebSocket Event Contract

Endpoint: `ws://<host>/ws/v1/adatrack?token=<JWT>` (origin validasi; ping server 30 s).

**Server → Client events:**
- `VEHICLE_UPDATE` — posisi real-time (payload FR-5.2: acc riil, fuel_level, satellites,
  altitude, gsm_signal, dst.).
- `MEDIA_EVENT` — media dashcam baru (model `MediaEventWS{Event, Data}`).
- `notify.alert.<vehicle_id>` — fan-out notifikasi alert (RBAC per client).
- `ERROR` — `{ event: "ERROR", error_code: "UNAUTHORIZED_VEHICLE", message: ... }`.

**Client → Server (subscription model):** subscribe `vehicle.update.{vehicle_id}`
(RBAC validated di server; reject 403 bila tidak diizinkan).

### 8.4 Rate Limiting (B2/B3)

- Login: `5` percobaan / 15 menit (lockout).
- API: `100` request / menit / user.
- WebSocket handshake: batasi per IP; max conn 5.000; queue 1.000 (drop-oldest + log).

### 8.5 Input Validation (WAJIB — seluruh input)

**Setiap input dari client (query param, path param, body JSON, form/multipart, header, WS
message) WAJIB divalidasi di server sebelum diproses:**

1. **Schema/binding validation:** struct `binding` tags (Gin `validate`/`binding:`) untuk semua
   DTO — required, format email, enum, min/max length, numeric range. Invalid → `400
   VALIDATION_ERROR` + daftar field gagal (tidak pernah lanjut ke query).
2. **Whitelist, bukan blacklist:** query param di-whitelist (mis. `from`/`to` harus RFC3339/date),
   enum hanya nilai terdaftar (mis. `area_type` ∈ circle|polygon).
3. **Parameterized SQL:** semua query parameterized (`$1,$2` / prepared) — tidak ada string
   concat dari input user (anti SQLi).
4. **Boundary check:** pagination `page`/`limit` dibatasi (max limit 1000); range date
   `from≤to`; ukuran body dibatasi (`http.MaxBytesReader`, `MEDIA_MAX_FILE_MB`); multipart
   count/files dibatasi.
5. **Encode output:** semua data yang dirender di HTML/JSON di-encode untuk anti-XSS;
   per-company data disaring server-side (tidak pernah percaya client) — anti IDOR/BOLA.
6. **Sanitasi protokol:** frame TCP di-parse strict dengan bounds-check (tidak ada panic pada
   input acak); IMEI 15-digit hanya digit.
7. **Fail-closed:** data tidak valid → ditolak + dirate-record + audit; **tidak ada** default
   permisif.

### 8.6 Security Request Flow — keamanan data saat request

Urutan wajib pada setiap request ter-autentikasi (REST/WS):
`TLS (WSS/HTTPS) → rate limit → CORS/origin validate → JWT verify (+no expiry, +revocation
check) → tenant resolution (company_code dari token, bukan dari body) → RBAC (tm_user_company_access
role_override) → row-level `tm_user_vehicles` filter → input validation (§8.5) → parameterized
query → response ter-encode + audit bila diperlukan`.

- **Sensitive fields** (password, token, secret) tidak pernah dikirim ke client / log
  (password: bcrypt hash only; refresh token: hash SHA-256 disimpan, raw hanya dikirim sekali).
- **Anti-replay/time check:** `iat/nbf/exp` JWT valid, clock-skew ≤ 30 s.
- **Audit** untuk aksi sensitif: LOGIN, 403, TOKEN_REVOKED, USER_CREATED, MEDIA_URL_ACCESS (§9.4).

---
## 9. Authentication, Authorization & Security

### 9.1 Authentication (JWT)

- **Login:** `POST /api/v1/auth/login` — email + bcrypt (cost 12) check di **master `tm_users`**.
- **JWT HS256:** payload `{ user_id, email, role, company_code, vehicle_ids?, iat, exp }`;
  expiry 24 h (`JWT_EXPIRY_HOURS`). Same claims kedua API (interop B2/B3).
- **Refresh/rotation/revocation (B4):** `POST /auth/refresh` (refresh token opaque 256-bit
  disimpan SHA-256 hash; rotasi wajib) + `POST /auth/logout` (denylist jti TTL).
  Revocation check in requireAuth → `401 TOKEN_REVOKED`. Env `JWT_REFRESH_EXPIRY_HOURS=168`,
  `JWT_REVOCATION_ENABLED=true`.
- **Rate limit login:** 5/15m; lockout; audit LOGIN_SUCCESS/FAILURE.
- Storage client: HttpOnly cookie recommended (not localStorage).

### 9.2 Authorization (RBAC)

- Every request: validate JWT → tenant context → resolve pool (master/company) →
  `tm_user_company_access` (role_override/is_active) → `tm_user_vehicles` row-level filter
  → `tm_role_menu_access` (akses menu per role — v1.7.0, B12).
- Cross-tenant access → `403`; platform scope enforcement (§3.1).
- Admin company sees all vehicles; others see assigned only (server-side filter, never client).

### 9.3 Security Checklist (GAP #12 + B4 §4.2)

1. **Authentication:** ☐ JWT HS256 · ☐ bcrypt cost 12 · ☐ rate limit login 5/15m
2. **Authorization:** ☐ tm_user_vehicles on every call · ☐ row-level security DB ·
   ☐ log all 403 (audit ACCESS_DENIED)
3. **API Security:** ☐ HTTPS/TLS (prod, WSS) · ☐ CORS allowlist dashboard domain ·
   ☐ rate limit 100 req/min/user · ☐ sanitize all inputs (parameterized statements —
   NO string concat) · ☐ output encoding anti-XSS · ☐ security headers
   (Permissions-Policy camera=(), dst.)
4. **DB Security:** ☐ encrypt sensitive data · ☐ minimal DB privileges · ☐ **audit trail wajib** `tm_audit_logs` (§9.4) · ☐ **soft delete** semua mutasi hapus (§6.0.1)
5. **Infra Security:** ☐ VPN for DB access · ☐ firewall only necessary ports ·
   ☐ secrets in env/vault (never commit) · ☐ CloudTrail/audit logs
6. **Incident Response:** ☐ on-call · ☐ runbook (`docs/INCIDENT_RUNBOOK.md`) · ☐ log all security events
7. **Device:** ☐ anti-spoofing IMEI allowlist · ☐ HMAC per-company media ingest
8. **Quality:** ☐ govulncheck scan · ☐ golangci-lint per service · ☐ versioned DB migrations · ☐ input validation coverage (100% endpoint, §8.5) · ☐ test audit trail + soft delete/restore (§16)
9. **Audit trail:** ☐ `tm_audit_logs` aktif semua service · ☐ append-only (tanpa UPDATE/DELETE) ·
   ☐ retry + dead-letter (no silent drop) · ☐ event wajib §9.4 lengkap ·
   ☐ akses data ter-soft-delete ter-audit (`SOFT_DELETED_VIEWED`)

### 9.4 Audit Trail (WAJIB — Audit Logging)

**Audit trail adalah requirement wajib, bukan opsional.** Setiap aksi sensitif (keamanan,
mutasi data, perubahan konfigurasi, provisioning) **wajib** menghasilkan satu baris audit
di `tm_audit_logs` master DB — **append-only**, tanpa `UPDATE`/`DELETE`.

**Skema baris audit:**

| Kolom | Isi |
|---|---|
| `audit_id` | PK (BIGINT identity) |
| `action` | `LOGIN_SUCCESS`, `LOGIN_FAILURE`, `LOGOUT`, `TOKEN_REFRESH`, `TOKEN_REVOKED`, `ACCESS_DENIED`, `USER_CREATED`, `USER_UPDATED`, `USER_SOFT_DELETED`, `PASSWORD_CHANGED`, `COMPANY_CREATED`, `TENANT_PROVISIONED`, `ADMIN_USER_AUTOCREATED`, `ENTITY_CREATED/UPDATED/SOFT_DELETED/RESTORED`, `SOFT_DELETED_VIEWED`, `IMEI_REGISTERED/REJECTED`, `MEDIA_URL_ACCESS`, `CONFIG_CHANGED`, `MIGRATION_APPLIED`, `HARD_DELETE` |
| `outcome` | `success` · `failure` · `denied` |
| `actor_*` | `actor_user_id`, `actor_email`, `actor_role`, `actor_ip`, `actor_user_agent` |
| `company_code` | konteks tenant (NULL untuk aksi platform) |
| `entity_type` / `entity_id` | entitas terdampak |
| `before` / `after` | JSON diff (field sensitif **ter-redaksi**: password, token, secret, HMAC) |
| `reason` | alasan (wajib untuk soft delete/restore/hard delete) |
| `request_id` | korelasi dengan log HTTP (`X-Request-ID`) |
| `created_at` | timestamp UTC |

**Aturan operasional:**
- **No silent drop:** kegagalan tulis audit → retry + backoff → `notification.deadletter` +
  metrik `audit_write_errors_total`; aksi sensitif gagal-audit → **fail-closed** (request ditolak).
- **Worker async** (buffer + batch insert) agar tidak menambah latency request; bounded buffer
  (anti memory-leak FR-4.4) dengan overflow → flush sinkron (bukan drop).
- **Retensi & immutability:** `tm_audit_logs` disimpan ≥ 1 tahun (partisi bulanan, §11);
  tidak ikut soft delete; hanya role `SuperAdmin` dapat membaca.
- **Korelasi:** `request_id` sama dengan log JSON service (structured logging) → trace satu request
  lintas service (API → NATS → worker).
- **Metrik & alert:** `audit_events_total{action,outcome}`, `audit_write_errors_total`;
  alert bila `LOGIN_FAILURE` / `ACCESS_DENIED` melonjak (potensi serangan, §9.6).
- **Verifikasi:** unit test memastikan setiap endpoint mutasi & setiap 401/403 menghasilkan
  tepat satu baris audit dengan `outcome` benar (§16).

### 9.5 TLS & Secrets

- TLS terminasi di proxy (Coolify/Caddy/Nginx); app Go serves HTTP internally.
- Secrets via `backend/.env` (gitignored) + `.env.example` (template). Prod: env/secrets manager.
- `JWT_SECRET` wajib unique per environment.

### 9.6 Aplikasi Aman dari Serangan (Application Security)

Sistem **wajib tahan terhadap serangan umum aplikasi web/API** (dipantau + diuji berkelanjutan):

| Serangan | Mitigasi Wajib |
|---|---|
| **SQL Injection** | 100% parameterized/prepared statement; input validation §8.5; scan `govulncheck` |
| **XSS (Cross-Site Scripting)** | Output encoding di semua rendering; header `Content-Security-Policy`; React default-escape |
| **CSRF** | CORS allowlist (dashboard-only); token/`SameSite=Strict/Lax` cookie; WS origin validation |
| **Brute-Force / Credential Stuffing** | Rate limit login 5/15m; lockout; bcrypt cost 12; audit LOGIN_FAILURE; optional 2FA (future) |
| **DoS / DDoS (request flood)** | Rate limit per IP/user; max conn/body/timeout; bounded buffers FR-4.4; backpressure; WAF (prod) |
| **IDOR / BOLA (horizontal access)** | Row-level `tm_user_vehicles`; tenant routing dari JWT (bukan body); forbidden → 403 |
| **JWT tampering / replay** | HS256 + secret kuat; `exp/nbf/iat`; revocation denylist jti (B4); clock-skew ≤ 30 s |
| **Spoofing device (fake IMEI)** | Anti-spoofing IMEI allowlist (`tm_vehicle_imei_map`) di ingestion (FR-1.4) |
| **Media forgery** | HMAC-SHA256 per-company (`X-Signature`) pada ingest media (FR-8.1) |
| **Ingress parse panic** | Strict parser with bounds-check (frame acak tidak menyebabkan panic); recovery middleware |
| **Secrets leak** | `.env.coolify` terpisah; `.gitignore`; vault/secrets manager prod; scan repo |
| **Dependency vulnerabilities** | `govulncheck` per service + image scan (Trivy) di CI |

**Keamanan pada traffic tinggi:** mitigasi DoS **tidak** mengorbankan latency — rate limit &
bounded buffer dievaluasi di load test peak (§16); line FR-4.4 (anti memory-leak/bottleneck)
dan §8.6 (security request flow) berlaku bersamaan.

---
## 10. Monitoring & Observability

### 10.1 Prometheus Metrics (per service — PRD §8.1)

**ingestion-tcp:** `tcp_connections_active/total`, `tcp_parse_errors_total`,
`tenant_resolution_duration_ms`, `tenant_lookup_errors_total`, `nats_publish_duration_ms`,
`nats_publish_errors_total`, `backpressure_drops_total`, `fuel_readings_total{protocol}`.

**worker-persistence:** `batch_insert_duration_ms`, `batch_insert_size`,
`batch_insert_errors_total`, `postgres_pool_connections_active`, `company_db_pool_count`,
`tenant_routing_duration_ms`, `retry_attempts_total`, `messages_processed_total{company_code}`,
`fuel_rows_inserted_total`, `fuel_rows_positionless_total`.

**worker-live:** `redis_update_duration_ms`, `redis_batch_size`, `redis_errors_total`,
`redis_pool_connections_active`, `vehicle_state_updates_total`, `odometer_updates_total`,
`engine_hours_updates_total`, `trip_events_total{company_code,event_type}`,
`stop_events_total{company_code}` (`trip_stop_flush_size`).

**worker-alert:** `alerts_generated_total{alert_type,severity}`, `alerts_processed_total{company}`,
`geofence_breach_duration_ms`, `overspeed_detected_total`, `sos_events_total`,
`sos_escalations_total`, `sos_time_to_acknowledge_seconds`, `fuel_alerts_total{alert_type}`,
`alerts_fuel_acc_suppressed_total{company}`, `notifications_sent_total{channel}`,
`notifications_pending_total`, `notification_delivery_duration_ms`, `notification_errors_total`,
`notification_retry_total`, `sms_provider_balance`, `notification_deadletter_total`.

**service-websocket / api-vehicle:** `ws_connections_active/total`, `ws_broadcast_duration_ms`,
`ws_message_queue_size`, `http_request_duration_ms`, `http_errors_total`,
`rbac_check_duration_ms`, `tenant_db_connections_active{company_code}`,
`tenant_routing_duration_ms`, `ws_message_*{subject,send}`.

**service-media:** `media_uploads_total{company_code,media_type}`, `media_upload_bytes_total`,
`media_presigned_total`, `media_cleanup_deleted_total`, `storage_objects{bucket}`,
`http_request_duration_ms`, `http_errors_total`.

**Shared/infra:** `db_read_queries_total{company_code,route}`, `db_replica_up{company_code}`
(read/write split), `go_*` (collector: goroutines, GC, process memory).

### 10.2 Health Checks (`/healthz` + `/metrics`)

**Readiness (before traffic):** TCP master DB ✓ · company DB pools ✓ · Redis ✓ · NATS ✓ ·
env completo ✓ · tenant resolution cache init ✓.

**Liveness (running):** master query < 5 s · company pool count = registered companies ·
NATS stable · worker no deadlock · tenant cache hit > 95%.

**SLO Recording:** uptime ≥ 99.9% (error budget 0,001), burn-rate alerts
(`monitoring/slo-rules.yml`); dashboard Grafana **"adatrack Core"** (73 panel / 11 section,
uid `adatrack-core`).

### 10.3 Alerting Rules (Prometheus — PRD §8.3)

| Condition | Severity | Action |
|---|---|---|
| TCP connections > 90% limit | WARNING | Scale up ingestion |
| NATS pending > 90% max | CRITICAL | Page on-call, check persistence |
| Insert latency > 10 s | CRITICAL | Check DB, query optimization |
| Tenant resolution latency > 500 ms | CRITICAL | Master DB slow |
| Company DB pool exhaustion > 90% | CRITICAL | Scale/investigate stuck pool |
| Tenant lookup error > 5% | WARNING | Unregistered IMEI flood |
| WS broadcast latency > 1 s | WARNING | Network; may drop users |
| Error rate > 5% | WARNING | Investigate root cause |
| Uptime < 99.9% | CRITICAL | Post-mortem |
| Notification failure > 5% | CRITICAL | SMTP/SMS gateway |
| Notification retry exhausted > 10/min | WARNING | Provider rate limit |
| SMS balance < 100 | WARNING | Top up |
| **`go_goroutines` tidak kembali ke plateau setelah peak** | WARNING | Indikasi goroutine leak (FR-4.4) — dump goroutine, restart worker |
| **Heap `go_memstats_heap_inuse_bytes` terus naik selama endurance** | WARNING | Indikasi memory leak (FR-4.4) — pprof, cek buffer unbounded |
| **HTTP latency p95 > 800 ms saat peak** | CRITICAL | Bottleneck (DB/Redis/NATS/broadcast) — periksa bagian §13/read-split |

### 10.4 Infra Monitoring (CPU/Memory/RDS — §4.1 B4)

- `node_exporter` (host CPU/mem/disk) · `cAdvisor` (per-container) · `postgres_exporter`
  (engine) · `prometheus + alertmanager` (targets via file_sd
  `monitoring/targets/adatrack-services.json`, gen `scripts/gen-prom-targets.sh`).
- Threshold: **CPU > 70% 5 min** WARNING · **> 85%** CRITICAL · **Memory > 80%** WARNING;
  RDS `DatabaseConnections > 80% max` CRITICAL; slow_queries/latency high → query SLA check.
- Grafana dashboard „adatrack Core" + SLO rules (Prometheus :9095 host publish).

### 10.5 Notification Delivery (PRD §8.4)

- Email via `NET SMTP` (TLS mandatory) + template engine (`text/template` + `html/template`),
  path `backend/services/worker-alert/templates/{alert_type}_{channel}.tmpl`; rate limit per company.
- SMS: Twilio (dev default) / AWS SNS (prod) via `SMS_PROVIDER`; E.164 format; fallback email bila SMS gagal.
- Retry backoff + dead-letter `notification.deadletter.{company_code}`; status tracking
  `pending → sent → delivered → failed → skipped`; `provider_response` JSON saved.

---
## 11. Data Retention & Archival Policy (GAP #4)

| Tier | Storage | Retention |
|---|---|---|
| **Live** | Redis | 5 minutes (real-time state only) |
| **Hot** | DB active (`th_telemetry_logs` current partitions) | 30 days (active queries) |
| **Warm (Archive)** | Object storage / archive | 31–180 days (quarterly reports) |
| **Cold** | Object storage | 180+ days — up to 7 years (compliance) |

**Purging Strategy:**
1. Drop oldest partition from DB monthly (after successful archive).
2. Archive to S3/object storage (compliance) — Parquet compressed, versioning enabled.
3. Verify row count before/after each purge (no silent data loss).

**Media (B5b, FR-8.7):** retention per company (`tm_company_media_config.retention_days`, default
30) — job harian `MEDIA_CLEANUP_CRON` hapus objek + status `expired`.

**JetStream (B4):** stream retention `MaxAge 48h` + `MaxBytes 4 GiB` (DiscardOld) via
`JETSTREAM_MAX_AGE_HOURS` / `JETSTREAM_MAX_BYTES` (semua stream wajib ter-limit).

**Audit & data ter-soft-delete (B11):**

| Data | Retention | Catatan |
|---|---|---|
| `tm_audit_logs` | **≥ 1 tahun** (partisi bulanan; compliance 7 tahun) | Append-only; **tidak** ikut soft delete; hanya `SuperAdmin` dapat membaca |
| Baris ter-soft-delete (`deleted_at IS NOT NULL`) | **180 hari** sejak `deleted_at` | Setelah itu job purge menghapus fisik + audit `HARD_DELETE` (§6.0.1) |
| Media ter-soft-delete | status `deleted` → job retensi menghapus objek (`expired`) | FR-8.7 |
| Permintaan GDPR (right-to-be-forgotten) | segera (≤ 30 hari) | Hard delete ter-audit (§15) |

---

## 12. Disaster Recovery & Backup Strategy (GAP #5 — B4)

| Layer | Backup | Retention | Targets |
|---|---|---|---|
| **DB (PostgreSQL)** | script backup DB (`backend/scripts/`, dump gzip per schema `adatrack_gps_*` + SHA256SUMS; pg_dump) | lokal 14 d | RTO 4 h · RPO 1 h; drill restore row-count match |
| **Restore** | script restore DB (`backend/scripts/`, verifikasi checksum + import + row count) | — | drill live (`adatrack_gps_restore_test` count match) |
| **Redis** | `backend/scripts/backup-redis.sh` (BGSAVE snapshot best-effort; RDB 6 h + AOF enabled; 3 snapshots) | ~5 min restore | Redis failure = 5 min stale state (acceptable) |
| **Config/Secrets** | env vault (HashiCorp Vault / AWS Secrets Manager) | auto | ops team only |

**Disaster Recovery Runbook:**
- **PostgreSQL down:** restore dari backup terbaru (4 h RTO).
- **Redis down:** restart (5 min, vehicles re-sync state dan live stream).
- **NATS down:** queue data lost ≤ buffered (acceptable ≤ 10k; JetStream persists rest).
- **Network partition:** graceful degradation (limited real-time updates).
- **Failover:** Redis drill live (`REPLICAOF NO ONE`); DB replicas = READ-SCALE only (not failover).
- **Restore data ter-soft-delete** (bukan bagian backup): `POST /api/v1/{resource}/{id}/restore`
  (§6.0.1) — Admin, ter-audit `ENTITY_RESTORED`; hard delete hanya job retensi/GDPR.

---

## 13. High Availability & Replication (docs/HIGH_AVAILABILITY.md)

**Model:** PRIMARY (tulis) + REPLICA (baca per-engine, via profile):

| Komponen | Replika | Prinsip |
|---|---|---|
| **PostgreSQL** | `postgres-replica` — streaming WAL + slot `pg_replica_slot` (standby read-only) | LIVE: walreceiver streaming, INSERT propagasi primary→replika, tulis direct ke replika ditolak |
| **Redis** | `redis-replica` — `replicaof` master | LIVE: failover cadangan state ≤5 min; drill promote + fail-back resync OK |

**Read/Write Split APP-LEVEL (B4):** `internal/tenant` — `Manager.ReadPool()` per-tenant
(best-effort warm), `ReadRouter` (Query/QueryRow → replica dengan fallback one-shot ke primary;
Exec selalu primary), breaker per-tenant (3 fail → open 30 s → half-open), prober berkala.
Wiring: service-websocket & api-vehicle (handler GET → companyRead), worker-alert (queries
baca), INSERT/guard dedup tetap primary. Metrik `db_read_queries_total{company_code,route}`,
`db_replica_up{company_code}`. Verified live PG :5533 + probe `cmd/db-replica-probe`
(READ→replica, WRITE→primary).

> **PENTING:** Replika DB **bukan** mekanisme backup dan **bukan** failover — proteksi data
> tetap via backup harian + uji restore. Skrip promote DB dihapus (keputusan 2026-08-25);
> Redis mempertahankan promote + drill.

---
## 14. Deployment & DevOps (GAP #7 + docs/DEPLOY_COOLIFY.md + PANDUAN_BACKEND.md)

### 14.1 Topologi Deployment

- **Dev/Staging (LOCAL):** `backend/docker-compose.yml` (canonical) — PostgreSQL + Redis + NATS
  (JetStream `-js`) + MinIO; entry point `backend/deployments/docker-compose.yml`
  (`include ../docker-compose.yml`, name `adatrack_gps_system`). Env: `backend/.env`.
- **Prod (Coolify):** **konfigurasi terpisah COOLIFY §7** — `backend/deployments/docker-compose.coolify.yml`
  + `backend/.env.coolify`. Dua strategi — (A) deploy per service via Dockerfile dengan konteks
  `backend/`; (B) compose stack. DB persisten via resource Coolify native; proxy/TLS Coolify.
- **HA overlay:** `backend/deployments/docker-compose.ha.yml` — replika per-engine + Redis,
  monitoring stack (Prometheus/Grafana/Alertmanager/exporters).
- **Tidak ada config campuran:** `compose-up.sh <local|coolify>` memilih varian; nilai dari
  `backend/.env` tidak pernah dipakai di environment Coolify dan sebaliknya (§7).

### 14.2 Service & Ports (reference)

| Service | Port HTTP/TCP | Healthz |
|---|---|---|
| `ingestion-tcp` | TCP 9003 (GT06, dev override) / 9011 Teltonika / 9002 TK103 | 8090 |
| `worker-live` | — | 8091 |
| `worker-persistence` | — | 8092 |
| `service-websocket` | HTTP 8082 (dev) / WS same | 8082 (/healthz) |
| `api-vehicle` | 18081 (dev) | 18081 |
| `worker-alert` | — | 8094 |
| `service-media` | :8095 | 8095 |
| Infra | PG :5533 · Redis :6380 · NATS :4222 · MinIO :9000/:9001 · Prometheus :9095 | — |

### 14.3 Build & Run (dev)

```bash
cd backend
docker compose up -d           # atau backend/scripts/compose-up.sh up -d
# migrasi otomatis (bootstrap + versioned): database/init-pg/ + database/migrations/ (§14.5)
go build -o <name> .            # tiap service, kontekst backend/ (module per service)
./scripts/start-services.sh     # helper dev (port-override + file_sd targets)
```

### 14.4 CI/CD (target)

- Pipeline per service: build + unit test + lint (`golangci-lint`) + security scan
  (`govulncheck`) + deploy (Coolify webhook / compose). Coverage gate ≥ 80% service inti.
- Database migrations versioned (`backend/database/migrations/`); auto-provision tenant
  meng-apply semua migration company idempoten.
- Config via environment; secrets manager (no commit credentials).

### 14.5 Migrasi Database Otomatis di Coolify (WAJIB)

**Setiap deploy ke Coolify WAJIB meng-apply migrasi otomatis dari repo — tanpa langkah manual.**

**Mekanisme:**

| Tahap | Aksi otomatis |
|---|---|
| **1. Build** | Dockerfile service (konteks `backend/`) menyalin `backend/database/` ke image (`init-pg/` + `migrations/`) sehingga artefak migrasi **selalu ikut versi kode** yang di-deploy (dari repo, bukan volume manual). |
| **2. Pre-deploy hook** | Coolify `pre_deployment_command` menjalankan `backend/scripts/migrate.sh`: tunggu DB ready (retry + backoff) → apply master migration → seed reference (idempoten) → verifikasi ledger. Gagal → **abort deploy** (fail-fast, tidak ada service jalan dengan schema lama). |
| **3. Boot service** | Setiap service meng-apply migrasi yang relevan saat start: master (`tm_schema_migrations`) dan/atau schema tenant. Konkurensi aman via PostgreSQL **advisory lock** (`pg_advisory_lock`) — satu migrator jalan, lainnya menunggu. |
| **4. Tenant baru** | Auto-provision company (FR-5.5) meng-apply **seluruh** migration company idempoten ke schema `adatrack_gps_{code}` + mencatat ledger. |
| **5. Verifikasi** | Ledger `tm_schema_migrations` diperiksa: `applied == jumlah file migration` & `failures = 0`; `/healthz` menolak ready bila migrasi belum lengkap. |
| **6. Audit & metrik** | Setiap migrasi tercatat audit `MIGRATION_APPLIED` (§9.4) + metrik `migration_applied_total{version,result}` & `migration_duration_ms`. |

**Prinsip:**
- **Idempoten:** semua migrasi aman dijalankan ulang (`IF NOT EXISTS`, `ON CONFLICT`, guard rename).
- **Versioned + checksum:** ledger menyimpan checksum file; migrasi yang sudah pernah jalan tidak
  boleh diubah isinya (drift → deploy ditolak) — perubahan selalu lewat file versi baru.
- **Satu arah (forward-only):** setiap migrasi `up`; rollback = deploy versi image sebelumnya
  + restore backup (§12), bukan `down` migration di produksi.
- **Config terpisah:** jalur migrasi membaca config dari **`.env.coolify`** (§7) dan **tidak pernah**
  menyentuh `.env` lokal.
- **Tanpa intervensi manual:** tidak ada `psql < file.sql` manual, tidak ada langkah "ingat apply
  migrasi" — memenuhi prasyarat "migrasi database di Coolify otomatis dari repo".

### 14.6 Checklist Verifikasi Deploy

1. `docker compose config` valid + infra healthy (PG/Redis/NATS/MinIO).
2. Migrations applied (master + company + reference seed) — row counts verify.
3. Semua service `/healthz` = ok; `/metrics` tersedia.
4. E2E smoke: loadtest frame valid → th_telemetry_logs delta; alert live; login JWT interop.
5. Monitoring targets file_sd (`scripts/gen-prom-targets.sh`) — 7/7 UP.
6. Backups scheduled + restore drill passes.
7. **Migrasi otomatis (§14.5):** ledger `tm_schema_migrations` → `applied == jumlah file` &
   `failures = 0`; re-run pre-deploy tanpa error (idempoten); boot tanpa migrasi lengkap →
   `/healthz` **not-ready**.
8. **Audit trail (§9.4):** `tm_audit_logs` menerima baris untuk login, 403, mutasi data,
   provisioning, dan `MIGRATION_APPLIED`; `audit_write_errors_total = 0`.
9. **Soft delete (§6.0.1):** endpoint `DELETE` tidak menghapus fisik (row + `deleted_at` ada);
   list default menyembunyikan; `?include_deleted=true` + restore bekerja (Admin, ter-audit).
10. **Onboarding tenant (FR-5.5):** create company → admin tenant otomatis ada (`Admin@123`,
    `must_change_password=true`) + seluruh migrasi schema ter-apply.

---

## 15. Compliance & Privacy (GAP #10)

1. **GDPR (bila di EU):** right to be forgotten (delete user + location history) —
   dijalankan sebagai **soft delete** (§6.0.1) lalu **hard delete ter-audit** oleh job khusus
   (audit `HARD_DELETE`, §9.4); data portability (export standard format); consent explicit
   opt-in tracking; data processing agreement dengan penyedia cloud.
2. **Data Encryption:** in transit HTTPS/TLS 1.3 (prod); at rest AES-256 (DB);
   Redis encryption optional (performance tradeoff).
3. **Access Logging & Audit Trail:** semua API access (who/when/what) → `tm_audit_logs`
   (§9.4 — append-only, retensi ≥ 1 tahun); log akses operasional retain 90 days;
   alert suspicious patterns (many 403).
4. **Privacy by Design:** minimal data collection (hanya field GPS yang diperlukan); data
   minimization (no ID numbers, etc.); purpose limitation (tracking only).
5. **Local:** Bahasa Indonesia interface; data wilayah Kemendagri (master reference);
   timezone Asia/Jakarta default.

---

## 16. Testing Strategy & Quality (GAP #11 + B4)

| Layer | Coverage Target | Status (2026-09-11) |
|---|---|---|
| Unit test | ≥ 80% service inti | worker-live 86,8% · worker-persistence 84,3% · api-vehicle 88,5% (worker-alert 80,0%+ B4) |
| Integration | 50% target (pipeline E2E) | E2E live tiap fase (fuel, media, alert, WS load) |
| E2E smoke | start all → 100 GPS packets → DB verify + WS verify + dashboard | per fase |
| Performance | load 400/2000 msg/s · WS load 50×1200 · endurance 24 h chunked | 39.941.163 msg 0 loss (2026-09-10) |
| Query SLA | count24h 11,6 ms · select30d 33,7 ms · geofence < 500 ms | |
| **Resource Stability** | p50 heap stabil / goroutine plateau setelah peak (FR-4.4) | endurance+peak wajib pantau `go_*`; bukti 0 leak/bottleneck saat 2000 msg/s |
| **Security & Validation** | 100% input divalidasi (§8.5); serangan umum ditolak (§9.6) | unit/integration: SQLi/XSS/IDOR/rate-limit/parameter test; `govulncheck` clean |
| **Audit Trail** | Setiap aksi sensitif → tepat 1 baris `tm_audit_logs` (§9.4) | test per-endpoint: 401/403/mutasi/provisioning menghasilkan audit `outcome` benar; kegagalan tulis → retry/DLQ |
| **Soft Delete** | `DELETE` = soft delete; restore berfungsi (§6.0.1) | test: row masih ada + `deleted_at`; list default menyembunyikan; `include_deleted=true` menampilkan; restore OK; audit tercatat |
| **Universal Protocol** | Semua brand GPS (Module 1c) | golden test per protokol (frame referensi 1:1) + registrasi IMEI + E2E; port per konvensi Traccar (Teltonika referensi sendiri) |
| **Tenant Onboarding** | Auto-admin `Admin@123` + `must_change_password` (FR-5.5) | test: create company → admin ada, login OK, endpoint lain 403 sampai ganti password |
| **Migrasi Otomatis** | Idempoten + ledger + fail-fast (§14.5) | test: apply ulang tanpa error; checksum drift → ditolak; boot tanpa migrasi lengkap → `/healthz` not-ready |

- Tooling: `go test -count=1`, `go vet`, `go build` (9/9 module hijau); mock infra
  (miniredis + mock DB driver); loadtest GT06 frame 1:1 parser (CRC-ITU valid);
  `backend/cmd/querybench` (10,3M rows seed) · `backend/cmd/loadtest-provision` (multi-tenant).

---
## 17. Non-Functional Requirements (NFR)

| Requirement | Target | Verification |
|---|---|---|
| **Latency** | < 800 ms end-to-end | Performance test 1000+ devices |
| **Throughput** | 2.000 msg/s peak (0 loss) | Load test sustained + spike |
| **Uptime** | 99.9% | SLO recording 30 days; error budget 0,001 |
| **Data Loss** | Zero tolerated | Audit trail + recovery procedures |
| **Database Query** | < 1,5 s (30-day history) | Benchmark data nyata saat B4 |
| **WebSocket Broadcast** | < 500 ms per 1000 users | Load dengan concurrent subscribers |
| **Resource Stability** | 0 memory-leak / bottleneck saat peak (2.000 msg/s) | Memory/goroutine profiling pada load test peak (FR-4.4) |
| **Input Validation** | 100% input ter-validasi | All-endpoint validation test (§8.5) |
| **Security** | Tahan serangan umum (SQLi/XSS/CSRF/DoS/IDOR/BOLA) | Security test + `govulncheck` + WAF (§9.6) |
| **Tenant Isolation** | 100% enforced | Verify no cross-tenant access in tests (incl. multi-tenant 1000 device load) |
| **Code Quality** | 80%+ test coverage | Unit + integration tests |
| **Documentation** | Complete API + deployment guide | PRD konsolidado + docs/ |

---

## 18. Risk Assessment & Mitigation

| Risk | Impact | Mitigation |
|---|---|---|
| Peak Traffic Spike | HIGH | NATS queue + backpressure + worker scaling (proven 2000 msg/s 0 loss) |
| Database Lock Contention | HIGH | Batch inserts + partitioning + pooling; read/write split replica |
| Tenant Isolation Breach | HIGH | JWT company_code validation + DB routing check every request + row-level tm_user_vehicles |
| Tenant Resolution Failure | MEDIUM | Master lookup fallback + retry + cache; reject unregistered IMEI (allowlist) |
| Redis Memory Overflow | MEDIUM | TTL (5 min) + eviction policy + monitoring |
| WebSocket Connection Drop | MEDIUM | Graceful reconnect + client-side retry (backoff 1/5/10 s) |
| GPS Device Disconnection | LOW | TCP keep-alive + heartbeat monitoring + OFFLINE alert |
| Data Retention Growth | MEDIUM | Partitioning + purge/archival per company; JetStream 48h/4 GiB |
| Konfigurasi/Koneksi DB Gagal | MEDIUM | Validasi env saat boot (fail-fast), pool bounded + reconnect otomatis, retry backoff, alert pool exhaustion (§10.3) |
| Device Spoofing | MEDIUM | IMEI allowlist anti-spoofing + audit `tenant_lookup_errors_total` |
| **Memory Leak / Goroutine Leak** | HIGH | Bounded buffers + worker count terbatas (FR-4.4); monitoring `go_*` + alerting; pprof di endurance |
| **Bottleneck saat Peak** | HIGH | Read/write split replica; bounded queues; rate-limit; load test peak berkelanjutan (§16) |
| **Serangan Aplikasi (SQLi/XSS/CSRF/DoS)** | HIGH | Parameterized SQL, output-encoding, CORS/CSRF, rate-limit, WAF, input validation (§8.5/§9.6) |
| **IDOR / Cross-Tenant Access** | HIGH | Row-level tm_user_vehicles + tenant routing dari JWT hanya; uji negatif tiap fase (403) |

---

## 19. Success Criteria (Acceptance)

- **Backend lengkap** — seluruh service multi-tenant (master + ≤50 company DB) B0–B6 selesai;
  B7 sub-fase odometer/engine-hours & trip/stop selesai (reverse geocoding & point reduction ⬜).
- **Throughput** — sustained 2.000 msg/s tanpa data loss (diverifikasi via load test +
  endurance 24 jam di B4).
- **Telemetry interval default 20 s** per device (nominal 250 msg/s untuk 5.000 device).
- **Stabilitas resource** — 0 memory leak / bottleneck saat peak (FR-4.4: goroutine plateau,
  heap stabil selama endurance).
- **Dashboard real-time** untuk seluruh vehicle yang berhak (tenant-isolated — diverifikasi
  via load test multi-tenant di B4).
- **Query 30-day history** < 1,5 s per company (benchmark saat B4).
- **Uptime 99.9%** target (SLO recording + dashboard Grafana).
- **Zero cross-tenant data access** (diverifikasi via test + load di B4).
- **Monitoring + alerting aktif** (Prometheus targets up + alert rules + SLO + runbook).
- **Audit trail lengkap** — setiap login, akses ditolak (403), mutasi data, perubahan konfigurasi,
  provisioning tenant, dan migrasi tercatat append-only di `tm_audit_logs` (§9.4); 0 silent drop.
- **Soft delete menyeluruh** — tidak ada `DELETE` fisik dari handler; semua penghapusan memberi
  `deleted_at` + ter-audit; restore tersedia (Admin) — diverifikasi test (§6.0.1).
- **Migrasi DB otomatis di Coolify** — deploy dari repo meng-apply migrasi (bootstrap + versioned,
  idempoten, ledger `tm_schema_migrations`, advisory-lock, fail-fast) tanpa langkah manual (§14.5).
- **Onboarding tenant 1 langkah** — `POST /api/v1/companies` (SuperAdmin) otomatis membuat 1 admin
  tenant (password default `Admin@123`, `must_change_password=true`) + seluruh migrasi schema (§4.2.1).
- **Dukungan semua brand GPS** — perangkat dari merek/model apa pun dapat di-onboard via referensi
  protokol Traccar + port per konvensi (200+ protokol); Teltonika memakai referensi sendiri (Module 1c).
- **Fuel sensor end-to-end** (B5a): `th_fuel_logs`, alert FUEL_DROP/REFUEL + notifikasi.
- **Dashcam event media** (B5b): HMAC ingest → MinIO → `th_media_events` RBAC → presigned →
  WS `MEDIA_EVENT` → retention.
- **Coverage ≥ 80%** pada service inti.
- **100% input divalidasi** (§8.5) dan **aplikasi tahan serangan umum** (§9.6) —
  SQLi/XSS/CSRF/DoS/IDOR ditolak.
- **Tipe bisnis B2B/B2C** — multi-tenant = B2B (§4.2); user master terpisah
  `tm_users` (B2B) / `tm_users_b2c` (B2C).
- **Normalisasi tabel `tm_`/`th_`/`td_`** ter-apply (B10).
- **Konfigurasi ganda LOCAL + COOLIFY** berjalan (compose + env terpisah, §7/§14).

---
## 20. Implementation Phases & Roadmap

### 20.1 Fase Backend (B0–B12)

> **Status Proyek:** Fase B0 telah **✅ Selesai**. Infrastruktur solid.
> Proyek dibangun dari titik nol. Pengerjaan akan dimulai secara berurutan dari fase **B0**
> (Infrastruktur + Foundations). Detail rencana & checklist: `.agent/03-backend-phases.md`.

| Fase | Area | Status |
|---|---|---|
| **B0** | Infrastruktur + Foundations (compose, migrations, internal pkg) | ✅ Selesai |
| **B1** | Pipeline Data: ingestion-tcp · worker-live · worker-persistence | 🟡 Stabil (Build & Core GT06/Teltonika OK) |
| **B2** | service-websocket: REST + WebSocket + RBAC + auto-provision company | 🟡 Stabil (REST API & WS Hub Tested) |
| **B3** | worker-alert + api-vehicle: GEOFENCE/OVERSPEED/BATTERY/OFFLINE/SOS/ROUTE_DEVIATION + notifikasi | ✅ Selesai |
| **B5a** | Fuel Sensor End-to-End (PRD v1.3.0 Module 7) | ✅ Selesai |
| **B5b** | Dashcam Event Media Scope A (Module 8) | ⚠️ Parsial (di api-vehicle) |
| **B4** | Performance, Monitoring, Testing, Hardening | ⚠️ Incomplete (Perlu Load Test Riil & Coverage) |
| **B6** | Real-Time Data Hardening (Audit Fix) | ✅ Selesai |
| **B7** | Fleet Management Core (B7.1 Odometer & Engine Hours · B7.2 Trip & Stop · B7.3 Reverse Geocoding · B7.4 Point Reduction) | ⚠️ Parsial (Odo/Trip OK, Geocoder perlu spatial DB lokal) |
| **B8** | Advanced Fleet Features (downlink, driver behavior, maintenance) | ✅ Selesai (Dynamic safety configs & Maintenance Cron) |
| **B9** | Protocol Expansion (Meiligao, Xexun, Suntech, H02, Totem, GT02, Navigil, Castel; validasi TK103) — port & decoding per referensi Traccar | ✅ Selesai (Generic decoding for 8 protocols) |
| **B10** | **Normalisasi & Konfigurasi** — prefix tabel `tm_`/`th_`/`td_` (migrasi rename idempoten), split user master `tm_users` (B2B) / `tm_users_b2c` (B2C), `business_type` di `tm_companies`, config ganda LOCAL + COOLIFY (`docker-compose.{local,coolify}.yml` + `.env.{local,coolify}`), telemetry interval 20 s, input validation + anti-attack hardening (§8.5/§9.6) | ⚠️ Parsial (Schema rename selesai, migration tool tenant iteration pending) |
| **B11** | **Governance & Data Lifecycle** — audit trail wajib `tm_audit_logs` (§9.4), soft delete global + endpoint restore (§6.0.1), auto-create admin tenant password `Admin@123` (FR-5.5), migrasi DB otomatis di Coolify (§14.5), dukungan protokol universal (Module 1c) | ⚠️ Parsial (Audit trail & soft delete parsial, auto-migration Coolify belum teruji) |
| **B12** | **Enterprise & Industry Modules** (acuan `docs/FRONTEND.md`, §5.10 Module 9) — drivers, groups, personel/kartu RFID/log akses, assets, maintenance, safety score & incidents, laporan/analitik lanjutan, organization, integrations (API/Webhook), share lokasi publik, heatmap; modul industry-specific (rental, transport, logistics, sales, field service, patrol, project site) bertahap; Personal/B2C (§4.2, FR-9.2); registry module & menu **master** (`tm_modules`/`tm_menus`, seed FRONTEND.md) + role menu access **per-tenant** (`tm_role_menu_access`, §6.2) | ⬜ Incomplete (78 baris stub 501, rute salah tempat) |

### 20.2 Fase Frontend (F1–F4 — menunggu backend selesai)

| Fase | Area | Status |
|---|---|---|
| **F1** | Scaffold Next.js + Tailwind + Map + i18n setup | ⬜ Not started |
| **F2** | Live Tracking Dashboard (map + list real-time) | ⬜ Not started |
| **F2.1** | Admin Monitoring (Prometheus/CPU/Mem/DB PostgreSQL widgets, ADMIN only) | ⬜ Not started |
| **F3** | History Playback, Geofence, Alerts UI + SOS UI | ⬜ Not started |
| **F4** | Auth/UX, Reporting, Polish + F4.1 i18n extensible | ⬜ Not started |

> **Aturan:** Frontend TIDAK boleh dimulai sampai seluruh fase backend B0–B6 selesai (B7
> optional continuing). Verifikasi tiap fase: build sukses, service boot, `/healthz` OK, data mengalir.
>
> **Struktur aplikasi (v1.7.0):** F1–F4 mengikuti struktur modul/menu `docs/FRONTEND.md`
> (Business & Personal + `packages/` shared). F1–F4 di bawah = inti tracking; menu lain
> (drivers, groups, akses, aset, keamanan, analisis, industry, administrasi lanjutan)
> dirilis bertahap menyusul backend **B12** (§5.10) — menu tanpa backend di-hide/stub.

### 20.3 Fitur Lintas-Fase (keputusan)

- **Tipe Bisnis (B2B/B2C)** — §4.2; multi-tenant = B2B; `tm_users` / `tm_users_b2c` (B10).
- **Notifikasi** (backend B3): channels WS/email/SMS per `tm_notification_preferences`; UI di F3/F4.
- **Route** (backend B3): create/assign/detection deviation; UI di F3.
- **SOS** (backend B3): CRITICAL + eskalasi + TTA; UI popup + ACK di F3 .
- **Fuel sensor** (backend B5a): kanal fuel-level real-time + th_fuel_logs + alert; UI grafik BBM di F3.
- **Dashcam media** (backend B5b): ingest → MinIO → catalog RBAC → WS MEDIA_EVENT; UI galeri di F3.
- **i18n** (F1 setup, F4.1 polish): next-intl, locale `id` + `en-US`, switcher, mapping error_code → locale string.
- **Struktur aplikasi Frontend (v1.7.0 — `docs/FRONTEND.md`):** Aplikasi Business (B2B) & Personal (B2C) + fitur lintas aplikasi (§3); backend wajib menyediakan API/data per menu (§5.10, B12); frontend F1 menyiapkan app shell dua aplikasi + navigasi menu per grup modul.

---
## 21. Known Gaps & Future Enhancements (Audit — PRD §13 + temuan_gemini)

### 21.1 A. Catatan Fitur Existing (Inkonsistensi & Kelengkapan)

1. **Reverse Geocoding belum diintegrasikan** — tabel wilayah (provinces/cities/districts/
   subdistricts) belum digunakan untuk resolusi alamat offline dan titik telemetri. (B7.3 ⬜)
2. **Keterbatasan protokol perangkat** — dari 200+ protokol Traccar, baru GT06/Teltonika/TK103
   aktif; TK103 provisional. Prioritas: Meiligao, Xexun, Suntech, H02 (B9).
3. **Codec 7 Teltonika DROPPED** — tidak didukung (error `unsupported codec 0x07`); decision
   doc: `docs/CODEC7_DECISION.md`.
4. **GT06 date encoding** kontradiktif di dokumen vendor → default plain-hex + toggle BCD
   (catatan jujur: verify device capture pre-production).
5. **querybench tool** — benchmark query historis (seed sintetis); cakupan terbatas pada skenario uji (catatan `docs/POSTGRES_PROVIDER.md`).

### 21.2 B. Fitur Esensial Fleet Management (roadmap)

| # | Fitur | Estimasi | Fase |
|---|---|---|---|
| 1 | **Downlink / Remote Commands** — connection registry, engine cut-off (`DYD#`), interval change, reboot | 5 d | ✅ Selesai (B8) |
| 2 | **Odometer & Engine Hours** | 2 d | B7.1 |
| 3 | **Trip & Stop Detection** | 5 d | B7.2 |
| 4 | **Driver Behavior Analysis** (harsh accel/braking/cornering — Teltonika IO 253/254, Concox alarm 0x09/0x0A) | 3 d | ✅ Selesai (B8) |
| 5 | **Maintenance Scheduling** (servis log, odometer/engine-hours reminders) | 3 d | ✅ Selesai (B8) |
| 6 | **Point Reduction** (Ramer–Douglas–Peucker utk history playback) | 2 d | B7.4 |
| 7 | **Reverse Geocoding** | 3 d | B7.3 |
| 8 | **Mobile App driver** (route accept, SOS, offline recording) | — | Phase 3+ |
| 9 | **Analytics & Reporting** (trip summary, violation summary, PDF/Excel, scheduled) | — | F4 |

### 21.3 C. Prioritas Implementasi (rekomendasi)

- **Jangka pendek:** ACC fix + DTO lengkap (B6).
- **Menengah:** odometer (B7.1) · trip/stop (B7.2) · reverse geocoding ⬜ · point reduction ⬜.
- **Menjelang produksi:** downlink commands · driver analysis · maintenance · protocol expansion.

---

## 22. Appendix

### 22.1 NATS Subject Conventions

| Subject | Deskripsi | Queue Group |
|---|---|---|
| `telemetry.raw.<IMEI>` | Raw telemetry (payload incl company_code) | persistence, live, alert |
| `telemetry.live.<IMEI>` | Live state update | websocket |
| `telemetry.error.<IMEI>` | Parse/processing error | — |
| `alert.geofence.*` · `alert.speed.*` · `alert.offline.*` · `alert.battery.*` · `alert.sos.*` | Alerts | websocket fan-out |
| `alert.fuel.<company>` | FUEL_DROP / REFUEL (B5a) | websocket fan-out |
| `alert.route_deviation.<company>` | Route deviation (B3) | websocket fan-out |
| `notify.alert.<vehicle_id>` | Notifikasi websocket per vehicle | websocket (RBAC) |
| `media.event.<company>` · `media.capture.request.<company>` | Dashcam event media (B5b) | websocket / media |
| `notification.deadletter.<company>` | Notification DLQ | ops |

### 22.2 Glossary / Akronym

**Tipe bisnis & multi-tenant**
- **B2B** (business-to-business) — tipe bisnis **default** platform: pelanggan = perusahaan
  (tenant), multi-tenant dengan schema per tenant `adatrack_gps_{code}`; auth via `tm_users` +
  `tm_user_company_access` (§4.2).
- **B2C** (business-to-consumer) — tipe bisnis single-tenant untuk konsumen/end-user; auth via
  `tm_users_b2c`; tanpa akses ke tenant B2B (rencana, non-blocking untuk B2B).
- **Multi-tenant** — satu platform melayani banyak perusahaan; isolasi data per tenant
  (`company_code` dari JWT → schema DB + Redis prefix), **implementasi = tipe bisnis B2B**.
- **Schema-per-tenant** — tiap tenant memperoleh schema PostgreSQL `adatrack_gps_{code}`;
  master pada schema `adatrack_gps_master`.

**Konvensi penamaan tabel (normalisasi)**
- **`tm_`** — tabel **master**: reference/registry/config relatif statis (mis. `tm_vehicles`,
  `tm_users`, `tm_geofences`, `tm_speed_configs`).
- **`th_`** — tabel **transaksi header**: satu baris "kepala" per kejadian (mis.
  `th_telemetry_logs`, `th_alerts`, `th_vehicle_trips`).
- **`td_`** — tabel **transaksi detail**: baris rincian anak ber-FK ke `th_*` (mis.
  `td_vehicle_stops`, `td_notifications`). Lihat §6.0 untuk mapping lengkap.

**Governance & data lifecycle (B11)**
- **Audit trail** — jejak audit **wajib** (§9.4): `tm_audit_logs` append-only mencatat aksi
  keamanan, mutasi data, perubahan konfigurasi, dan provisioning; retensi ≥ 1 tahun.
- **Soft delete** — penghapusan logis: `deleted_at`/`deleted_by`/`delete_reason` (§6.0.1);
  `DELETE` fisik dilarang dari handler; restore via endpoint khusus.
- **Hard delete** — penghapusan fisik hanya oleh job retensi (§11) atau GDPR (§15), selalu ter-audit.
- **Universal protocol support** — dukungan semua brand GPS tanpa terkecuali via referensi
  Traccar (Module 1c); satu-satunya pengecualian = Teltonika (referensi sendiri).

**Umum**
GPS, IMEI, TCP, NATS (JetStream), Redis, PostgreSQL, RBAC, TTL, SOS, SLA, SLO, RPO,
RTO, FR, WS, HMAC, MinIO/S3, GT06/Concox, Teltonika (Codec 8/8E), TK103, BBM (bahan bakar
mineral), ACC (ignition/contact status), TTA (time-to-acknowledge), DLQ (dead-letter queue),
IDOR/BOLA (insecure direct object reference / broken object level authorization).

### 22.3 Dokumen Terkait (index)

- `docs/INCIDENT_RUNBOOK.md` — **baca saat insiden** (I1–I12 + eskalasi + recovery checklist)
- `docs/HIGH_AVAILABILITY.md` — HA & DR strategi lengkap + read/write split
- `docs/POSTGRES_PROVIDER.md` — provider PG implementasi + keterbatasan
- `docs/DATABASE_ARCHITECTURE.md` — DB single-instance vs split decision
- `docs/FRONTEND.md` — struktur modul/menu/fitur aplikasi Frontend (Business & Personal) — **acuan penerapan backend** (§5.10 Module 9 + roadmap B12)
- `docs/device-connection-guide.md` — koneksi device GPS (setup + framing test)
- `docs/docs-device/` — protokol vendor (GT06 v1.8.1/v3.1) + traccar-reference (200+ protokol,
  incl. `08-teltonika-codec8.md` STANDALONE)
- `.agent/` — global rules, roadmap, phases detail (aturan kerja repo)
- `PRD.md` — PRD konsolidated (v1.3.0 + GAPS + FEATURE) — satu sumber kebenaran
- `docs/DEPLOY_COOLIFY.md` — deploy Coolify (§14.5 migrasi otomatis + `.env.coolify`)
- `docs/PANDUAN_BACKEND.md` — panduan menjalankan/verifikasi service backend
- `docs/CODEC7_DECISION.md` — keputusan Codec 7 Teltonika (DROPPED)

---
