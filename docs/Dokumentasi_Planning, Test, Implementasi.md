# Product Requirement Document (PRD) 
## Real-Time GPS Tracking & Adatrack Management System

> ## ⭐ DOKUMEN
>
> `Dokumentasi_Planning, Test, Implementasi.md` ini adalah **satu-satunya sumber kebenaran (single source of truth)** bagi
> platform Real-Time GPS Tracking & adatrack Management. Dokumen ini **berdiri sendiri** — semua
> spesifikasi produk, definisi kebutuhan fungsional, skema database, konfigurasi, monitoring,
> roadmap implementasi, risiko, kriteria sukses, dan lampiran (gap tracking + cakupan fitur) ada di
> dalamnya tuntas dan **tidak bergantung pada dokumen lain**.
>
> ### Cakupan (Scope) Produk
> Real-time adatrack management berlatensi < 800 ms & uptime 99.9%: **≤50 tenant** perusahaan
> (multi-tenant; PostgreSQL default = schema-per-tenant `adatrack_gps_{COMPANY_CODE}` dalam 1
> physical DB, MySQL selectable = database-per-tenant), **≤5.000 device GPS** aktif
> mengirim telemetry tiap 5 detik (≤1.000 device target realistis; ~200–400 msg/s avg/peak),
> **500–1.000 pengguna dashboard** ber-RBAC. Pipeline: ingestion TCP (GT06/Concox/Teltonika/TK103)
> → NATS (`telemetry.raw.<IMEI>`) → worker-live (Redis live-state) + worker-persistence
> (PostgreSQL default / MySQL, partitioned batch) → service-websocket (REST + WebSocket RBAC) + worker-alert
> (geofence/overspeeding/battery/offline/SOS/route-deviation/fuel). Ekstensi v1.3.0:
> fuel sensor (Modul 7) & dashcam event media (Modul 8, scope A). Live streaming video
> (WebRTC/RTSP) eksplisit **OUT OF SCOPE** pada versi ini.
>
> ### Navigasi
> §1 Ringkasan · §2 Personas & RBAC · §3 Arsitektur & Alur Data · §4 Functional Requirements
> (Ingestion → Live → Persist → WS → Dashboard → Fuel → Media) · §5 Skema Database · §6
> Konfigurasi & Environment · §7 Monitoring, Observability & Notifikasi · §8 Fase/Roadmap ·
> §9 NFR · §10 Risiko · §11 Kriteria Sukses · **Lampiran A** Gap Tracking · **Lampiran B**
> Feature Coverage · **Catatan Operasional** (panduan jalankan & keputusan dev).

---
## 1. Document Overview & Metadata

* **Project Title:** Real-Time GPS Tracking & adatrack Management Platform
* **Document Version:** v1.3.0 (Updated with Fuel Sensor & Dashcam Event-Media Extensions)
* **Status:** Approved for Engineering
* **Target Audience:** Engineering Team (Backend, Frontend, DevOps), Product Managers, QA Team, System Architects
* **Tech Stack Overview:** 
  * **Ingestion Layer:** Golang (TCP Listener)
  * **Message Queue / Broker:** NATS
  * **Cache & Live State:** Redis
  * **Persistent Database:** **PostgreSQL 16 (default proyek)** — driver `pgx` v5;
  MySQL 8.0 (InnoDB / Partitioned) tetap tersedia & dapat dipilih via `DATABASE_PROVIDER=mysql`
  (lihat §7.1.1). Kedua engine didukung: partition by timestamp, parameterized SQL, connection pooling.
  * **Real-Time Engine:** Golang WebSocket Server
  * **Object Storage:** MinIO (dev, S3-compatible) / AWS S3 / OSS (prod) — dashcam event media (v1.3.0)
  * **Frontend Web:** Next.js (React), TailwindCSS, Mapbox GL / Leaflet

---

## 2. Executive Summary & Objectives

### 2.1 Problem Statement
Perusahaan pengelola armada kendaraan (logistik, rental, transportasi publik) membutuhkan sistem pemantauan posisi dan status armada secara real-time dengan latensi sangat rendah (< 1 detik). Banyak sistem legacy mengalami bottleneck saat menangani ribuan perangkat GPS yang mengirimkan data telemetry secara bersamaan setiap 5–10 detik.

### 2.2 Product Vision
Membangun platform *Real-Time adatrack Management* berkinerja tinggi, berskala besar (*scalable*), dan *fault-tolerant* yang mampu mencerna (*ingest*) puluhan ribu paket data GPS per detik, menampilkan pergerakan armada secara *smooth* di peta, serta memberikan notifikasi bahaya/geofence secara *real-time*.

### 2.3 Key Success Metrics (OKRs)

| Metric | Target | Notes |
|--------|--------|-------|
| **Telemetry Latency** | < 800 ms | End-to-end: GPS device send → Dashboard display |
| **System Uptime** | 99.9% | = ~8.64 hours downtime per year |
| **Peak Device Capacity** | 5,000+ total (across all companies) | Realistic target: <1,000; Message frequency: every 5 seconds |
| **Number of Companies** | ≤50 concurrent tenants | Multi-tenant: **PostgreSQL** = schema-per-tenant (`adatrack_gps_{COMPANY_CODE}`) dalam 1 physical DB (default proyek); **MySQL** = database-per-tenant (`adatrack_gps_{COMPANY_CODE}`) |
| **Dashboard Users** | 500–1,000 concurrent users | Role-based access (not all see all vehicles) |
| **Database Query Performance** | < 1.5 seconds | Playback 30 days of history |
| **Throughput** | 200 msg/sec (avg), 400 msg/sec (peak) | ≤1,000 devices × 1 msg/5 sec |

---

## 3. User Personas & Roles

| Role | Description | Core Goals | Vehicle Access | Dashboard Users |
| :--- | :--- | :--- | :--- | :--- |
| **System Admin** | Mengelola tenant, akun pengguna, pendaftaran unit GPS baru. | Konfigurasi sistem, manajemen user & permission. | **ALL 5000+ vehicles** | < 50 |
| **adatrack Manager** | Memantau armada, analisis efisiensi rute & konsumsi BBM. | Monitoring ketersediaan, laporan mingguan. | **Assigned adatrack (50-500)** | 500-2000 |
| **Dispatcher/Operator** | Memantau pergerakan armada real-time. | Menanggapi alerts, geofence breach, emergency. | **Assigned adatrack (50-500)** | 1000-2000 |
| **Driver** | Pengemudi kendaraan dengan GPS tracker. | Menerima rute, SOS darurat. | **Own vehicle only** | 1000-1500 |

### 3.1 Role-Based Access Control (RBAC) Model

**Access Control Strategy:**
- **Database Layer:** 
  - `users` table: Stores user credentials + role
  - `user_vehicles` junction table: Maps user → vehicles they can access
  - Every query filtered by: `WHERE vehicle_id IN (user's assigned vehicles)`

- **API Layer:**
  - Every endpoint must validate JWT token + user role
  - Check `user_vehicles` before returning vehicle data
  - Return 403 Forbidden if user tries to access unauthorized vehicle

- **WebSocket Layer:**
  - User subscribes to vehicle topics: `vehicle.update.{vehicle_id}`
  - Server checks permission before accepting subscription
  - Only push updates to users who have access to that vehicle

**Benefits:**
- ✅ Reduces broadcast load (not all users see all 5000 vehicles)
- ✅ Improves latency (smaller message fan-out)
- ✅ Increases security (users only see their own adatrack)
- ✅ Simplifies dashboard queries (pre-filtered by role)

---

## 4. System Architecture & Data Flow

```text
┌─────────────────────────────────────────────────────────────────┐
│                  GPS DEVICES (≤1,000 total, ≤50 companies)                    │
│            Sending 1 message every 5 seconds                     │
└────────────────────────┬────────────────────────────────────────┘
                         │ TCP:9000
                         │
         ┌───────────────▼─────────────────┐
         │  ingestion-tcp (1 instance)     │
         │  ✅ DONE                         │
         │  - Accept 5000 connections      │
         │  - Parse binary protocol        │
         │  - Tenant resolution: IMEI →    │
         │    company_code (master DB)     │
         │  - Enrich payload w/ company_   │
         │    code, publish to NATS        │
         └───────────────┬─────────────────┘
                         │ NATS pub: telemetry.raw.<IMEI>
                         │  (payload includes company_code)
         ┌───────────────▼──────────────────┐
         │  NATS Queue (1 instance)        │
         │  - MaxPending: 10,000 msgs      │
         │  - Backpressure enabled         │
         └──────┬──────────────┬───────────┘
                │              │
    NATS Sub    │              │ NATS Sub
         ┌──────▼──┐      ┌────▼─────────┐
         │worker-  │      │worker-       │
         │live:1   │      │persistence:1 │
         │(Redis)  │      │(DB Batch)    │
         │✅ DONE  │      │✅ DONE       │
         └──────┬──┘      └────┬─────────┘
                │              │
         ┌──────▼──────────────▼───────┐
         │  Redis (1 instance)        │
         │  PostgreSQL 16 (default)   │
         │  / MySQL 8.0 (selectable) │
         │  ┌─ adatrack_gps_master       │
         │  │ companies, countries,  │
         │  │ cities, districts,     │
         │  │ subdistricts, users (auth)│
         │  └─ adatrack_gps_{COMPANY}   │
         │  │ user_company_access │
         │  │ vehicles, user_veh,    │
         │  │ telemetry_logs, etc.   │
         │  └─ adatrack_gps_default     │
         │  - Live state cache        │
         │  - Per-tenant telemetry    │
         └──────┬──────────────────────┘
                │
    ┌───────────▼─────────────────┐
    │service-websocket (1)        │
    │⚠️ IMPLEMENT                  │
    │- REST API                   │
    │- WebSocket server           │
    │- RBAC filtering             │
    │- Tenant-aware DB routing    │
    └───────────┬─────────────────┘
                │
    ┌───────────▼──────────────────────────────┐
    │  DASHBOARD USERS (500–1,000)            │
    │  ├─ Admin (see all vehicles)             │
    │  ├─ Managers (see assigned adatrack)        │
    │  ├─ Drivers (see own vehicle)            │
    │  └─ Operators (see assigned adatrack)       │
    └──────────────────────────────────────────┘
```

> **Multi-Tenant Notes:**
> - **Master** (`adatrack_gps_master`): reference data (companies, countries, cities, districts, subdistricts) + authentication (users).
> - **Company** (`adatrack_gps_{COMPANY_CODE}`): per-tenant isolation + `user_company_access` (per-company role/permissions, ref ke `master.users.id`). 
>   - **PostgreSQL (default proyek):** satu physical DB + **schema** per tenant (`adatrack_gps_master`, `adatrack_gps_{code}`) dipilih via `search_path` pada koneksi.
>   - **MySQL (selectable):** database fisik per tenant.
>   Dibuat otomatis ketika company didaftarkan di master (lihat §7.1.1).
> - **Default** (`adatrack_gps_default`): tenant/schema fallback bawaan sebelum ada company pertama didaftarkan.
> - Setiap service (worker-persistence, service-websocket, api-vehicle) melakukan **dynamic DB routing** berdasarkan `company_code` dari payload/telemetry atau JWT.

---

## 5. Functional Requirements (FR)

### Module 1: Ingestion & Device Communication (Go TCP + NATS)

**FR-1.1:** Server harus membuka TCP Port terdedikasi untuk menerima koneksi socket dari perangkat GPS (misal: protokol GT06, Teltonika, Concox, TK103).

**FR-1.2:** Server harus mampu memparse paket biner (HEX/ASCII) menjadi format JSON standar:
```json
{
  "imei": "864201040512345",
  "company_code": "ABLE01",
  "latitude": -6.2088,
  "longitude": 106.8456,
  "speed": 45.5,
  "heading": 180,
  "altitude": 125.5,
  "acc_status": 1,
  "gprs_signal": 4,
  "battery_level": 85,
  "timestamp": "2026-08-16T10:30:00Z"
}
```

**FR-1.3:** Mengirimkan ack response (handshake/ping ack) kembali ke perangkat GPS agar koneksi socket tetap terhubung (keep-alive). Timeout: 90 second idle, 3 minute offline.

**FR-1.4:** Mempublikasikan data yang telah diparse ke NATS Subject `telemetry.raw.<IMEI>`. Sebelum publish, lakukan **tenant resolution**: lookup IMEI → company_code via master database (`adatrack_gps_master`). Tambahkan field `company_code` ke payload JSON telemetry.

**FR-1.5 (NEW):** **Backpressure Handling**
- Check NATS pending message count before publishing
- If pending > 50% of max (5000 msgs), log warning + optionally shed non-critical fields
- If pending > 90% of max (9000 msgs), drop telemetry packets + log error
- MUST log every drop for observability + debugging

### Module 1a: GT06 / Concox Protocol — Implementasi & Catatan Jujur

Sumber acuan: `docs/docs-device/GT06_GPS_Tracker_Communication_Protocol_v1.8.1.md` dan
`docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md`.

**Status implementasi di `ingestion-tcp` (B1):**
- ✅ **Framing** mendukung `0x78 0x78` (length 1 byte) dan `0x79 0x79` (length 2 byte).
- ✅ **Error Check CRC-ITU (CRC-16)**: dihitung dari byte `Packet Length` s/d `Information
  Serial Number`, tabel & algoritma persis Appendix v3.1; **terverifikasi terhadap 4 contoh
  biner di dokumen** (`D9DC`, `8CDD`, `E1A0`, `9FF8`). (Sebelumnya kode memakai checksum
  additive yang SALAH.)
- ✅ **Login** `0x01` → parse IMEI 15-digit + tenant/anti-spoofing.
- ✅ **Position/Location** `0x12`/`0x22`: decode Date-Time, satellites (nibble),
  **latitude/longitude = raw ÷ 1.800.000** (metode menit × 30000), speed, course 10-bit,
  hemisphere dari course/status; parse tail v3.1 (ACC, mileage) secara defensif.
- ✅ **Heartbeat** `0x13` (dan `0x23` EG02/EG03) → ack + publish keep-alive.
- ✅ **Alarm** `0x26`/`0x27`(HVT)/`0x19`(LBS): parse GPS + battery + GSM + alarm code → publish.
- ✅ **Time check** `0x8A` → balas waktu server (v3.1 §9.2).

**⚠️ Ambiguitas & kekurangan yang DICATAT JUJUR (dari dokumen vendor):**
1. **Encoding tanggal (kritis).** Dokumen Concox *hanya* mendecode satu contoh tanggal secara
   eksplisit (`0x0A 0x03 0x17 0x0F 0x32 0x17` → 2010-03-23 15:50:23) yang hanya konsisten dengan
   **plain-hex** (nilai byte == digit desimal: `0x17`=23, `0x32`=50). Dua contoh paket lain juga
   hanya masuk akal sebagai plain-hex (`0x2E`=46 menit, `0x1D`=29 hari). Namun **banyak firmware
   Concox nyata memakai BCD** (`0x17`=17). Karena kontradiktif, implementasi memakai **plain-hex
   default** dengan toggle `GT06_DATE_BCD=true` untuk firmware BCD (PRD §7.1).
2. **Konten paket location berbeda antar versi dokumen.** v1.8.1 §5.2.1 (26 byte content, berakhir
   di Cell ID) vs v3.1 §3.1 (33 byte, menambah ACC/UploadMode/GPSRealTime/Mileage). Implementasi
   memparse prefix 18-byte GPS yang identik, lalu tail extended *defensif* (hanya bila byte cukup).
   Digunakan `Mileage` bila 4 byte tersedia; belum diverifikasi ke device nyata.
3. **Contoh CRC paket location di v3.1 (`80 81`) tidak cocok** dengan algoritma CRC-ITU yang sama
   dokumen itu paparkan — dianggap contoh ilustratif/typo (empat contoh lain cocok). Beri tanda:
   verifikasi ulang dengan packet capture device nyata sebelum produksi.
4. **Position response**: v1.8.1 mengharapkan ack; v3.1 §3.2 menyatakan "server no response".
   Implementasi mempertahankan ack `0x05` (kompatibel dengan generasi lama & guide koneksi).
5. **Model terdaftar (v3.1):** JM-VL02/VG03/VG04, EG02/EG03, JM01, JV200, GT300, GT800, MT200,
   OB22, X3, Q2, GT08, Wetrack lite, ET25, HVT001.

### Module 1b: Multi-Protokol — Teltonika & TK103

Sistem ingestion kini **multi-port**: setiap protokol mendapat listener TCP sendiri (GT06 port
`TCP_PORT`=9000, Teltonika `TELTONIKA_TCP_PORT`=9001, TK103 `TK103_TCP_PORT`=9002; `0`=nonaktif).
Pendekatan ini menghindari sniffing header yang rapuh antar framing.

**Teltonika (Codec 8/8E + Codec 7)** — `controllers/teltonika.go`:
- ✅ Login: `4-byte length + IMEI ASCII`, balas `0x01`.
- ✅ AVL packet: `4-byte length | CodecID | count | records | CRC-16(LE) | count`.
- ✅ Codec 8/8E & Codec 7 record: timestamp(ms), priority, lon/lat ÷1e7, altitude, angle,
  satellites, speed(=10×km/h), IO elements (id/len/value). IO 72→battery, 66/67→ACC.
- ✅ Balas jumlah record (codec-8 convention).
- ⚠️ **JUJUR:** repo **tidak memuat dokument Teltonika resmi**; offset & CRC-16 (poly 0x1021,
  init 0) berdasar protokol publik. **Harus diverifikasi dgn dokument vendor & capture device.
  Battery & beberapa IO disimpan mentah (belum pemetaan penuh).**

**TK103 (PROVISIONAL)** — `controllers/tk103.go`:
- Keluarga TK-103 sangat terfragmentasi & tak punya satu spesifikasi baku terbuka.
- Implementasi = **subset frame GT-clone umum** (`0x78 0x78 | len | cmd | XOR | 0x0D 0x0A`):
  login (`0x12`), heartbeat (`0x13`), position (`0x22`), alarm (`0x26`).
- ⚠️ **JUJUR:** status **belum lengkap / provisional**. LAT/LON & alarm memakai layout GT06 clone
  dan **belum diverifikasi ke device nyata**. Wajib dapatkan dokumen vendor TK-103 + packet
  capture sebelum produksi. Jika device TK-103 Anda adalah GT-clone murni, bisa langsung dipakai.

### Module 1c: B0 Audit — Laporan Terverifikasi (2026-08-22)

Hasil **audit B0 terperinci** yang sudah **diverifikasi menjalankan** (bukan hanya baca):

**Fondasi yang sudah benar:**
- ✅ Paket shared `ajb_gps/internal` (config+dotenv, slog-JSON, metrics Prometheus,
  mysqlclient, natsclient, redclient, tenant) lengkap & dipakai semua 6 service.
- ✅ Migrasi schema ada & konsisten: master 001–009, company 001–012, legacy_single_db.
- ✅ Infra dev **live & healthy** (docker): MySQL `8.0`, Redis `7-alpine`, NATS `2.10-alpine -js`.
  Multi-tenant DB ter-init: `adatrack_gps_master`, `adatrack_gps_default`, `adatrack_gps_dev001`.
- ✅ Tidak ada Dockerfile nyasar di root `services/` (isu B0 sudah bersih).

**Kekurangan yang DITEMUKAN & DIPERBAIKI dalam audit ini:**
- ⚠️ **FIX GAP 1 — Sumber kebenaran compose dev.** Tidak ada `backend/docker-compose.yml`.
  → **Dibuat** (`backend/docker-compose.yml`) sebagai single source of truth dev
  (MySQL/Redis/NATS + healthcheck + JetStream), dan `backend/deployments/docker-compose.yml`
  kini me-delegasi via `include: ../docker-compose.yml` (tanpa drift). Keduanya lolos
  `docker compose config --quiet` (VALID).
- ⚠️ **FIX GAP 3 — Dockerfile service.** Hanya 3 service punya Dockerfile, dan **3 Dockerfile
  yang ada TIDAK buildable** (hanya `COPY main.go` / `COPY *.go`, melewatkan `controllers/` &
    `models/`, dan `replace ajb_gps/internal => ../../internal` butuh konteks monorepo).
  → Semua 6 Dockerfile ditulis ulang dengan **build context `backend/`** (monorepo):
    `docker build -f services/<name>/Dockerfile .` dari `backend/`.
  → **Diverifikasi build sukses**: `adatrack-ingestion-tcp:test` ✅ dan `adatrack-worker-live:test` ✅.
- ✅ **E2E ingestion multit:** GT06 & Teltonika diverifikasi live terhadap infra (lihat
  keterangan di bawah).
- ⚠️ **GAP 2 (open) — env tidak konsisten.** `NATS_SUBJECT_PREFIX` & beberapa `MASTER_DB_*`
  masih tidak dipakai semua service (sebagian hardcode `telemetry`). → audit per-service
  tersisa di B0 (daftar di bawah).

**Status E2E yang tervalidasi (dengan infra live + `ingestion-tcp` host):**
- Infra: `docker compose up -d` → MySQL/Redis/NATS `healthy`; init multi-tenant OK.
- `ingestion-tcp` boot dengan 3 listener: `:9000` (gt06), `:9001` (teltonika), `:9002` (tk103),
  `/healthz` = ok, tenant pool PRE-warm DEV001/LOGI002/TEST01.
- GT06 login+position (IMEI terdaftar `864201040512345`) → ACK login `78 78 04 01 …000…`,
  ACK position `78 78 03 05 00 02 …` → NATS `telemetry.raw.864201040512345` payload
  `{"lat":-6.2088,"lon":106.8456,"speed":45,"satellites":9,"company_code":"DEV001","vehicle_id":1}`.
- Teltonika IME login+codec8 → ack `00 00 00 01 01` → NATS payload codec8
  `{"lat":-6.2088,"lon":106.8456,"speed":8,"heading":180,"satellites":11,"acc":true}`.

---

### Module 2: State Management & Caching (Redis)

**FR-2.1:** Menyimpan posisi dan status terkini (last known position) setiap kendaraan pada Redis Hash Key:
```
Key: adatrack_gps:{company_code}:vehicle:state:<IMEI>
Value: {
  lat: float,
  lon: float,
  speed: float,
  status: enum[ONLINE, OFFLINE, IDLE],
  timestamp: unix_timestamp,
  battery: int
}
TTL: 5 minutes (offline detection)
```
> **Tenant isolation:** key prefix `adatrack_gps:{company_code}:` mencegah collision antar company di Redis yang sama.

**FR-2.2:** Menyimpan status koneksi socket (Online/Offline/Idle) berdasarkan heartbeat interval:
- Online: Last message received < 90 seconds
- Idle: Last message received 90s - 3 minutes
- Offline: No message > 3 minutes

**FR-2.3 (NEW):** **Batch Redis Updates for Performance**
- Instead of: 1 message → 1 Redis write
- Do: Buffer 100 messages → 1 MGET + 1 MSET per 100ms
- Result: 1000 msg/sec → 10 batch ops/sec (100x reduction in Redis ops)

**FR-2.4 (NEW):** **Redis Connection Pooling**
- Min Pool Size: 10
- Max Pool Size: 30
- Connection Timeout: 5 seconds
- Idle Timeout: 5 minutes

---

### Module 3: Storage & Persistence Engine (PostgreSQL 16 default / MySQL 8.0 selectable)

> Engine persisten dipilih per deployment via `DATABASE_PROVIDER` — **PostgreSQL default proyek**,
> MySQL tetap tersedia (`DATABASE_PROVIDER=mysql`). Semua FR di bawah berlaku untuk kedua engine;
> titik cabang SQL (placeholder `$N` vs `?`, upsert `ON CONFLICT` vs `ON DUPLICATE KEY UPDATE`,
> `RETURNING id` vs `LastInsertId`) dikapsulkan di paket `internal/dialect` (detail §7.1.1).

**FR-3.1:** Bulk/Batch Insert dari NATS Queue ke engine persisten (PostgreSQL default / MySQL)
secara periodik (tiap 5 detik ATAU per 500 records, mana yang lebih dulu).

**FR-3.2:** Tabel `telemetry_logs` harus di-partition berdasarkan RANGE (timestamp) untuk:
- Mempercepat query history playback
- Proses pembersihan data lama (data purging)
- Reduce lock contention

**FR-3.3 (NEW):** **Database Connection Pooling**
- Min Connections: 20 (default; env `*_POOL_MIN`)
- Max Connections: 50 (configurable based on worker count; env `*_POOL_MAX`)
- Max Idle Time: 5 minutes
- Statement Timeout: 30 seconds
- Pool sizing: PostgreSQL `POSTGRES_POOL_MIN/MAX`; MySQL `MYSQL_POOL_MIN/MAX`.

**FR-3.4 (NEW):** **Batch Insert Implementation**
```
Worker-Persistence Algorithm:
1. Read up to 500 messages from NATS queue
2. Wait max 5 seconds OR until 500 records buffered
3. Prepare batch INSERT statement (prepared statements for performance)
4. Execute INSERT INTO telemetry_logs (...) VALUES (...), (...), ... [500x]
5. Ack all 500 messages to NATS
6. If INSERT fails, NACK all 500 + retry with exponential backoff (1s, 5s, 10s)
7. If all retries exhausted, publish nack to error queue + log for investigation
```

**FR-3.5 (NEW):** **Database Indices for Query Performance**
```sql
-- Primary key: partition + timestamp
PRIMARY KEY (id, timestamp)

-- For vehicle history queries
CREATE INDEX idx_device_time ON telemetry_logs (imei, timestamp DESC);

-- For spatial queries (geofence)
CREATE SPATIAL INDEX idx_location ON telemetry_logs (location);

-- For aggregations
CREATE INDEX idx_timestamp ON telemetry_logs (timestamp);
```

---

### Module 4: Flow Control & Backpressure Strategy (NEW SECTION)

**FR-4.1 (NEW):** **NATS Queue Configuration**
```
MaxPending Messages: 10,000 (= ~25 second buffer at 400 msg/sec peak)
MaxInflight per Subscriber: 100
Message TTL: None (persist until ACK/NACK)

NATS Subject Convention (multi-tenant):
- telemetry.raw.<IMEI>          → raw telemetry (payload includes company_code)
- telemetry.live.<IMEI>          → live state update (payload includes company_code)
- telemetry.error.<IMEI>         → parse/processing error
- alert.geofence.{company_code}.{geofence_id}
- alert.speed.{company_code}.{vehicle_id}
- alert.sos.{company_code}.{IMEI}
- alert.battery.{company_code}.{vehicle_id}
- alert.offline.{company_code}.{vehicle_id}
Queue Groups: persistence, live, websocket, alert
```

**FR-4.2 (NEW):** **Backpressure Signaling**
```
TCP Ingestion Layer:
  Check pending count before publish
  If pending > 50% → Log warning, optionally drop ACC status updates
  If pending > 90% → Drop telemetry, log error
  
Worker-Persistence Layer:
  If INSERT latency > 5 sec → Log warning
  If INSERT latency > 10 sec → NACK message + re-queue (exponential backoff)
  
WebSocket Layer:
  If broadcast queue > 10,000 messages → Start dropping non-critical updates
  Broadcast only to users in same company_code (tenant isolation)
  Log every drop for observability
```

**FR-4.3 (NEW):** **Error Recovery Strategy**
- Retry logic: Exponential backoff (1s, 5s, 10s)
- Max retries: 3
- If all retries fail: Publish to error queue + log + alert ops team
- DO NOT silently drop data without logging

---

### Module 5: Real-Time Streaming (Go WebSocket + RBAC)

**FR-5.1:** Menerima subscribe dari akun pengguna sesuai hak akses armada (RBAC Filter) + tenant isolation.
- Validate JWT token → extract `company_code` + `user_id` + `role`
- Validate user existence di **master `users` table** (auth authority)
- Resolve role/permissions per company via **`user_company_access`** di `adatrack_gps_{company_code}` (role_override bila ada, permissions JSON)
- Query `user_vehicles` di **company DB** untuk dapatkan assigned vehicle IDs
- Hanya izinkan subscribe ke `vehicle.update.{vehicle_id}` — **reject 403** bila company_code atau vehicle_id tidak diizinkan

**FR-5.2:** Mengirimkan data pembaruan lokasi via WebSocket connection ke client setiap kali data baru tersedia.
- Message frequency: Every time new data arrives on NATS (avg 5 seconds per vehicle)
- Format:
```json
{
  "event": "VEHICLE_UPDATE",
  "data": {
    "imei": "864201040512345",
    "company_code": "ABLE01",
    "plate_number": "B 1234 XYZ",
    "lat": -6.2088,
    "lon": 106.8456,
    "speed": 45.2,
    "heading": 180,
    "acc": true,
    "status": "MOVING",
    "battery": 85,
    "timestamp": "2026-08-16T10:30:00Z"
  }
}
```

**FR-5.3:** Memiliki mekanisme reconnection logic otomatis pada client jika koneksi WebSocket terputus.
- Server: Send ping setiap 30 seconds
- Client: Auto-reconnect with exponential backoff (1s, 5s, 10s)
- Client: Resume subscription after reconnect

**FR-5.4 (NEW):** **WebSocket Connection Pooling & Resource Limits**
- Max concurrent connections: 5000+ (configurable)
- Send buffer per connection: 256KB
- Max message queue per connection: 1000
- If queue exceeds 1000, drop oldest message + log warning

**FR-5.5 (NEW):** **REST API — Company Tenant Registration + Auto-Provision**
- `POST /api/v1/companies` — Admin-only endpoint untuk mendaftarkan company baru.
- Body: `{ "code": "ABLE01", "name": "Company Name", "country_code": "ID", "timezone": "Asia/Jakarta" }`
- Otomatis membuat area tenant `adatrack_gps_{company_code}` (PostgreSQL: schema; MySQL: database) + applying migration template `tenant.Config.MigrationsDirFor()` (memilih `company_pg/` atau `company/`).
- Response `201 Created`: `{ "code", "name", "country_code", "timezone", "database_name", "migrations_applied" }`
- Jika company code sudah terdaftar, dilakukan upsert + re-apply migrations (idempotent).
- CLI alternatif: `migrate-tenant provision -code ABLE01 -name "Company Name" -country ID -tz Asia/Jakarta`

---

### Module 6: Interactive Web Dashboard (Next.js)

**FR-6.1:** Live Tracking Map dengan indikator warna:
- 🟢 **Hijau:** Bergerak (Speed > 0, ACC ON)
- 🟡 **Kuning:** Berhenti (Speed = 0, ACC ON)
- ⚫ **Hitam:** Offline (No message > 3 min)
- 🔴 **Merah:** Alert/Geofence breach

**FR-6.2:** Real-time list dengan vehicle status, location, speed

**FR-6.3:** History Playback: Tampilkan route 24 jam dengan timeline scrubber

**FR-6.4:** Geofence Management: Create/edit/delete zones, set alert rules

**FR-6.5 (NEW):** **Dashboard Query Performance**
- "Get all vehicles I can access": < 1 second
- "Get vehicle history 30 days": < 1.5 seconds
- "Get vehicles in geofence": < 500ms (with spatial indices)

---

### Module 7: Fuel Sensor Integration (NEW v1.3.0)

> Sumber protokol resmi: `docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md`
> ("0D Fuel sensor data", contoh frame CRC `0D12`) dan GT06 v1.8.1 (perintah cut-off BBM
> `DYD`/`HFYD`). Implementasi backend di fase **B5a** (`.clinerules/03-backend-phases.md`).
> Keputusan produk 2026-08-23 (user-approved): fitur ini wajib ada untuk enterprise.

**FR-7.1:** Ingestion kanal bahan bakar — `ingestion-tcp` mem-parse paket fuel sensor GT06
protocol number `0x0D` (frame long `79 79`; payload = blok waktu 6-byte + string ASCII sensor,
mis. `!AILOIL,<count>,<fuel1>,<fuel2>,<temp>,...`). Frame tak-dikenal tetap dicatat (structured
log + counter), tidak pernah silent-drop.

**FR-7.2:** Teltonika — seluruh elemen AVL IO dikumpulkan generik; mapping IO-ID → semantik
bahan bakar dikonfigurasi via env (`TELTONIKA_IO_FUEL_LEVEL`, default `86`; opsional
`TELTONIKA_IO_FUEL_USED`, `TELTONIKA_IO_FUEL_TEMP`) sehingga mendukung sensor eksternal FLS
maupun CAN-bus tanpa perubahan kode.

**FR-7.3:** Payload standar `telemetry.raw.<IMEI>` diperluas field opsional `fuel_level`
(persen 0–100), `fuel_volume` (liter), `fuel_temp_c` (°C). Field absen ≠ nilai nol (semantik
pointer + omitempty).

**FR-7.4:** Persistensi — semua pembacaan fuel disimpan ke tabel ter-partisi bulanan
`fuel_logs` (company DB); baris telemetry ber-posisi tetap masuk `telemetry_logs` tanpa
perubahan skema. Baris fuel-tanpa-posisi TIDAK masuk `telemetry_logs` (dicatat counter
`fuel_rows_positionless_total`, tanpa silent-drop).

**FR-7.5:** Live state (`worker-live`) — pesan parsial fuel-only melakukan *merge* ke Redis
`{prefix}{company}:vehicle:state:{IMEI}` (posisi tidak tertimpa); pesan normal tetap fast-path
MSET. `fuel_level` ikut tampil pada live state/API/WebSocket.

**FR-7.6:** Alert — **FUEL_DROP** (critical; penurunan > threshold dalam window sambil ACC ON
→ indikasi pencurian BBM) dan **REFUEL** (info; kenaikan > threshold); threshold
per-vehicle/global via tabel `fuel_configs`; dedup open-alert + grace window; publish
`alert.fuel.<company_code>`; tipe notifikasi baru `fuel_drop`/`refuel` mengikuti
`notification_preferences` per user/channel.

**FR-7.7:** REST API — `GET /api/v1/vehicles/{id}/fuel/history?from&to` (RBAC row-level
`user_vehicles`, format respons/error GAP #3) dan CRUD `/api/v1/fuel-configs`
(role Admin/adatrack Manager).

**FR-7.8:** Kalibrasi kurva voltase→volume ditunda (out-of-scope core v1.3.0); skema disiapkan
agar dapat dikembangkan dari `fuel_configs` tanpa breaking change.

---

### Module 8: Dashcam Event Media (NEW v1.3.0 — Scope A: event snapshot & clip)

> Keputusan produk 2026-08-23 (user-approved): cakupan **event media** — foto + video pendek
> saat trigger sos/alarm/geofence/overspeed/manual/scheduled/power — disimpan di object storage
> S3-compatible (**MinIO** dev / AWS S3-OSS prod). **Live streaming video (WebRTC/RTSP)
> eksplisit OUT OF SCOPE** fase ini (bisa jadi fase lanjutan). Implementasi: layanan baru
> `backend/services/service-media` (module `ajb_gps/service-media`) pada fase **B5b**.

**FR-8.1:** Ingest media peristiwa via HTTP `POST /api/v1/media/events` (multipart ATAU JSON +
presigned PUT flow) dengan autentikasi **HMAC-SHA256 per-company** (header `X-Signature`;
secret per company di master `company_media_config.hmac_secret`).

**FR-8.2:** Object storage via abstraksi shared `internal/storage` (interface `Store`):
layout key `{company}/{vehicle}/{yyyyMM}/{uuid}`; allowlist content-type `image/jpeg`,
`video/mp4`; ukuran maksimum per-file per-company (`max_file_mb`).

**FR-8.3:** Katalog per company DB — tabel `media_events` dengan siklus status
`uploaded → available → expired | failed`.

**FR-8.4:** REST API ber-RBAC (JWT interop B2/B3, tenant routing + filter `user_vehicles`
row-level): list/detail media, `GET /media/{id}/url` → presigned URL TTL pendek (+audit),
soft-delete (Admin saja).

**FR-8.5:** Real-time — publish NATS `media.event.<company_code>`; `service-websocket`
fan-out sebagai event WS `MEDIA_EVENT` hanya ke klien company sama yang berhak atas vehicle
tersebut (pola `notify.alert` yang sudah ada).

**FR-8.6:** Capture request — saat alert critical/SOS tercipta, `worker-alert` publish
`media.capture.request.<company_code>` (perintah capture via GT06 online-command `0x80` utk
model yang mendukung; fallback pasif = dashcam push otomatis saat alarm lokalnya; dukungan
per-model firmware divalidasi bertahap — dicatat jujur).

**FR-8.7:** Retensi — job harian menghapus objek lebih tua dari `retention_days` per company
dan menandai `status='expired'` (selaras GAP #4 retention/archival policy).

**FR-8.8:** Metrik & health — `media_uploads_total{company,type}`, `media_upload_bytes_total`,
`media_presigned_total`, `media_cleanup_deleted_total`, `storage_objects{bucket}`;
`/healthz` = ping object storage + pool DB + NATS.

---

## 6. Database Schema

> **Multi-Tenant Architecture:** Sistem memakai **tenant-per-company** dengan dua model penyimpanan
> (dipilih via `DATABASE_PROVIDER`, §7.1.1):
> - **PostgreSQL 16 (default proyek):** satu physical database + **schema per tenant**
>   (`adatrack_gps_master`, `adatrack_gps_{COMPANY_CODE}`), dipilih via `search_path` pada DSN koneksi.
> - **MySQL 8.0 (selectable):** database fisik per tenant (`adatrack_gps_{COMPANY_CODE}`).
> Pada kedua model berlaku: satu area master/dataset global & otentikasi, dan satu area per company
> (`adatrack_gps_{COMPANY_CODE}`) untuk data operasional.
> - **Master DB / Schema** (`adatrack_gps_master`): `companies`, `countries`, `cities`, `districts`, `subdistricts`, `users` (**otoritas autentikasi** — password hash disimpan sekali di sini), `vehicle_categories`, `vehicle_types` (master referensi kendaraan enterprise-standard — universal referensi untuk semua company), `vehicle_imei_map` (lookup IMEI → company_code untuk tenant resolution).
> - **Company DB / Schema** (`adatrack_gps_{COMPANY_CODE}`): `user_company_access` (per-company role/permissions, ref ke `master.users.id`), `vehicles`, `user_vehicles`, `telemetry_logs`, `geofences`, `alerts`, `speed_configs`, `geofence_vehicles`, `notification_preferences`, `notifications` (audit trail), `routes`, `route_assignments`.
> - **Default DB / Schema** (`adatrack_gps_default`): area/tenant fallback bawaan, dipakai sebelum ada company pertama didaftarkan.
> - **Login flow:** auth via **master** (email → company_code + password verify). Semua request setelah login route ke `adatrack_gps_{company_code}` sesuai `company_code` di JWT.
> - **No super-admin:** setiap company punya admin lokal via `user_company_access.role_override`. Tidak ada akun global yang bisa akses semua company (cross-tenant access ditolak 403).
> - **Auto-provision:** ketika company baru didaftarkan di master (`companies`), sistem otomatis membuat schema/database `adatrack_gps_{COMPANY_CODE}` + applying migration template (`tenant.Config.MigrationsDirFor()` memilih `company_pg/` atau `company/` otomatis).
> - **Tenant resolution:** `ingestion-tcp` lookup IMEI → company_code via `master.vehicle_imei_map` (bukan via company DB, karena belum diketahui company-nya).
> - **Cross-tenant references:** semua FK ke `users` (user_id, created_by, acknowledged_by, dst.) mereferensi `master.users.id` **tanpa FK constraint** melintasi database/schema — di-manage via `user_company_access.user_id` (tabel lokal di area company yang berisi `user_id` + role + permissions).
> - **Naming convention:** area perusahaan = `adatrack_gps_{LOWERCASE(company_code)}` (mis. company code `ABLE01` → `adatrack_gps_able01`). PostgreSQL: schema name (lowercase, konsisten). MySQL: nama database (lowercase wajib — `lower_case_table_names=0`).

### 6.1 Master Database Schema (`adatrack_gps_master`)

Database ini menyimpan data referensial global + registry user. Setiap company berhambatan ke database ini untuk **tenant resolution** (IMEI lookup) dan referensi master data.

```sql
-- Companies Table (Tenant Registry)
-- Master DB — tiap company = satu tenant. country_code FK ke countries.
-- Enterprise-standard tenant registry: legal entity, kontak resmi, tax id,
-- audit trail (created_by/updated_by) dan soft delete (deleted_at).
CREATE TABLE companies (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    code VARCHAR(20) UNIQUE NOT NULL,               -- e.g. "ABLE01", "LOGI002"
    name VARCHAR(100) NOT NULL,
    legal_name VARCHAR(255) NULL,                    -- enterprise: nama badan hukum
    company_email VARCHAR(255) NULL,                 -- enterprise: email kontak resmi
    website VARCHAR(255) NULL,                       -- enterprise: URL situs web
    tax_id VARCHAR(50) NULL,                         -- enterprise: NPWP / VAT number
    postal_code VARCHAR(10) NULL,                    -- enterprise: kode pos alamat
    country_code VARCHAR(2) NOT NULL,               -- ISO 3166-1 alpha-2, e.g. "ID", "MY", "US"
    address TEXT,                                    -- perusahaan physical address
    phone VARCHAR(20),                             -- kontak telepon perusahaan
    timezone VARCHAR(50) DEFAULT 'Asia/Jakarta',   -- IANA timezone, e.g. "Asia/Jakarta"
    settings JSON,                                   -- retention_policy, max_devices, dll.
    is_active BOOLEAN DEFAULT TRUE,
    activated_at TIMESTAMP NULL DEFAULT NULL,        -- enterprise: timestamp aktivasi tenant
    created_by BIGINT NULL DEFAULT NULL,             -- enterprise: audit creator (users.id)
    updated_by BIGINT NULL DEFAULT NULL,             -- enterprise: audit modifier (users.id)
    deleted_at TIMESTAMP NULL DEFAULT NULL,          -- enterprise: soft delete (NULL = aktif)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (country_code) REFERENCES countries(iso_code),
    INDEX idx_code (code),
    INDEX idx_country (country_code),
    INDEX idx_active (is_active),
    INDEX idx_tax_id (tax_id),
    INDEX idx_deleted (deleted_at)
);

-- Countries Table (Master Reference Data)
-- ISO 3166-1 — referensi statis untuk company registration + multi-country ops.
CREATE TABLE countries (
    id INT AUTO_INCREMENT PRIMARY KEY,
    iso_code VARCHAR(2) UNIQUE NOT NULL,           -- ISO 3166-1 alpha-2, e.g. "ID", "MY", "US"
    iso_code_3 VARCHAR(3) UNIQUE,                  -- ISO 3166-1 alpha-3, e.g. "IDN", "MYS"
    name VARCHAR(100) NOT NULL,
    phone_code VARCHAR(10),                       -- e.g. "+62", "+1", "+44"
    currency_code VARCHAR(3),                     -- ISO 4217, e.g. "IDR", "USD", "MYR"
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_iso_code (iso_code),
    INDEX idx_active (is_active)
);

-- Cities Table
-- Province ditambahkan untuk negara dengan administrasi provinsi (mis. Indonesia, US, Australia).
CREATE TABLE cities (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    country_id INT NOT NULL,
    name VARCHAR(100) NOT NULL,
    province VARCHAR(100),                          -- provinsi/negara bagian (untuk negara dengan provinsi)
    latitude DECIMAL(10, 8),                        -- center point untuk map rendering
    longitude DECIMAL(11, 8),                       -- center point untuk map rendering
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (country_id) REFERENCES countries(id),
    INDEX idx_country (country_id),
    INDEX idx_name (name)
);

-- Districts Table
CREATE TABLE districts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    city_id BIGINT NOT NULL,
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10),                       -- kode pos
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (city_id) REFERENCES cities(id),
    INDEX idx_city (city_id),
    INDEX idx_name (name),
    INDEX idx_postal (postal_code)
);

-- Subdistricts Table
-- Di Indonesia: kelurahan/ desek. postal_code + lat/lng berguna untuk address validation & geofencing context.
CREATE TABLE subdistricts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    district_id BIGINT NOT NULL,
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10),                       -- kode pos (biasanya di level kelurahan di Indonesia)
    latitude DECIMAL(10, 8),                       -- center point untuk geofencing context
    longitude DECIMAL(11, 8),                       -- center point untuk geofencing context
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (district_id) REFERENCES districts(id),
    INDEX idx_district (district_id),
    INDEX idx_name (name),
    INDEX idx_postal (postal_code)
);

-- Users Table (GLOBAL Auth Authority)
-- Password hash disimpan sekali di sini. Company DB hanya referensi user_id via user_company_access.
-- Login flow: email → company_code + password verify di master.users.
-- Enterprise-standard identity (SCIM-aligned): username terstruktur, nama
-- depan/belakang, verifikasi email/telepon, MFA, account lockout, locale,
-- audit trail (created_by/updated_by) dan soft delete (deleted_at).
CREATE TABLE users (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    company_id BIGINT NOT NULL,                     -- FK ke companies.id
    company_code VARCHAR(20) NOT NULL,               -- denormalisasi kecepatan lookup
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,            -- bcrypt cost 12
    full_name VARCHAR(100) NOT NULL,
    username VARCHAR(191) UNIQUE NULL,               -- enterprise: SCIM userName
    first_name VARCHAR(100) NULL,                    -- enterprise: given name
    last_name VARCHAR(100) NULL,                     -- enterprise: family name
    phone_number VARCHAR(20) NULL,                   -- enterprise: E.164
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,   -- enterprise: flag verifikasi email
    phone_verified BOOLEAN NOT NULL DEFAULT FALSE,   -- enterprise: flag verifikasi telepon
    mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,      -- enterprise: multi-factor auth
    password_changed_at TIMESTAMP NULL DEFAULT NULL, -- enterprise: perubahan password terakhir
    failed_login_attempts INT NOT NULL DEFAULT 0,    -- enterprise: counter lockout (GAP #12)
    locked_until TIMESTAMP NULL DEFAULT NULL,        -- enterprise: lockout expiry
    locale VARCHAR(10) NOT NULL DEFAULT 'id',        -- enterprise: preferensi locale i18n
    avatar_url VARCHAR(512) NULL,                    -- enterprise: foto profil
    role ENUM('Admin', 'Manager', 'Operator', 'Driver') NOT NULL,
    status ENUM('active', 'inactive', 'suspended') NOT NULL DEFAULT 'active',
    last_login TIMESTAMP NULL,
    created_by BIGINT NULL DEFAULT NULL,             -- enterprise: audit creator (users.id)
    updated_by BIGINT NULL DEFAULT NULL,             -- enterprise: audit modifier (users.id)
    deleted_at TIMESTAMP NULL DEFAULT NULL,          -- enterprise: soft delete (NULL = aktif)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (company_id) REFERENCES companies(id),
    INDEX idx_company (company_id),
    INDEX idx_company_code (company_code),
    INDEX idx_email (email),
    INDEX idx_username (username),
    INDEX idx_role (role),
    INDEX idx_locked (locked_until),
    INDEX idx_locale (locale),
    INDEX idx_deleted (deleted_at)
);

-- Vehicle IMEI Map (for tenant resolution di ingestion-tcp)
-- Master lookup table: IMEI → company_code
-- Diperbarui otomatis saat vehicle didaftarkan di company DB
CREATE TABLE vehicle_imei_map (
    imei VARCHAR(30) PRIMARY KEY,                    -- device IMEI
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
       FOREIGN KEY (company_code) REFERENCES companies(code),
    INDEX idx_company (company_code),
    INDEX idx_vehicle (vehicle_id)
);

-- Vehicle Categories (Master Reference Data — enterprise-standard)
-- Enterprise-standard categories (ISO 377-2019 / UNECE aligned):
--   PVB — Passenger Vehicle (cars, SUVs, hatchbacks)
--   LCV — Light Commercial Vehicle (≤ 3.5 t GVW)
--   MCV — Medium Commercial Vehicle (3.5 – 12 t GVW)
--   HCV — Heavy Commercial Vehicle (> 12 t GVW)
--   TW  — Two-Wheeler (motorcycles, scooters, mopeds)
--   THW — Three-Wheeler (auto-rickshaws, tricycles)
--   EV  — Electric Vehicle (any propulsion class)
--   SPV — Special Purpose Vehicle (fire trucks, ambulances, excavators)
CREATE TABLE vehicle_categories (
    id            BIGINT AUTO_INCREMENT PRIMARY KEY,
    code          VARCHAR(20) UNIQUE NOT NULL,           -- e.g. "PVB", "LCV", "HCV", "TW", "SPV"
    name          VARCHAR(100) NOT NULL,                  -- e.g. "Passenger Vehicle"
    name_local    VARCHAR(100) NOT NULL,                  -- e.g. "Kendaraan Penumpang"
    description   TEXT,
    min_gvw_kg    DECIMAL(10,2),                         -- GVW minimum (kg); NULL = tidak terbatas bawah
    max_gvw_kg    DECIMAL(10,2),                         -- GVW maksimum (kg); NULL = tidak terbatas atas
    display_order INT DEFAULT 0,                          -- urutan tampilan di UI dropdown
    is_active     BOOLEAN DEFAULT TRUE,
    created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_code          (code),
    INDEX idx_active        (is_active),
    INDEX idx_display_order (display_order)
);

-- Vehicle Types (Master Reference Data — sub-classes within a category)
-- Setiap tipe mereferensi vehicle_categories(category_id).
-- fuel_types adalah SET yang mendeskripsikan bahan bakar yang didukung tipe tersebut.
CREATE TABLE vehicle_types (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    category_id     BIGINT NOT NULL,                       -- FK → vehicle_categories.id
    code            VARCHAR(30) UNIQUE NOT NULL,          -- e.g. "SEDAN", "SUV", "PICKUP_TRUCK"
    name            VARCHAR(100) NOT NULL,               -- e.g. "Sedan"
    name_local      VARCHAR(100) NOT NULL,               -- Bahasa Indonesia, e.g. "Sedan"
    description     TEXT,
    typical_gvw_kg  DECIMAL(10,2),                        -- GVW typisan (kg) untuk type ini
    fuel_types      SET('petrol','diesel','electric','hybrid','CNG','LPG','hydrogen'),
    is_active       BOOLEAN DEFAULT TRUE,
    display_order   INT DEFAULT 0,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES vehicle_categories(id) ON DELETE RESTRICT,
    INDEX idx_category       (category_id),
    INDEX idx_code           (code),
    INDEX idx_active         (is_active),
    INDEX idx_display_order  (display_order)
);

-- ============================================================================
-- Company Media Config (v1.3.0 — Modul 8 Dashcam Event Media, MASTER DB)
-- Konfigurasi object-storage & ingest per company; hmac_secret dipakai untuk
-- validasi header X-Signature pada endpoint ingest service-media (FR-8.1).
-- ============================================================================
CREATE TABLE IF NOT EXISTS company_media_config (
    company_code   VARCHAR(20) PRIMARY KEY,
    bucket         VARCHAR(63)  NOT NULL,                -- bucket/object-store per company
    retention_days INT          NOT NULL DEFAULT 30,     -- retensi objek media (GAP #4)
    max_file_mb    INT          NOT NULL DEFAULT 100,    -- batas ukuran per file
    hmac_secret    VARCHAR(128) NOT NULL,                -- secret HMAC ingest dashcam/gateway
    created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);
```


### 6.2 Company Database Schema (`adatrack_gps_{COMPANY_CODE}`)

Database ini dibuat otomatis per company. Semua data operasional tersimpan di sini — terisolasi penuh antar company. Tabel `user_company_access` adalah **registry user lokal** (ref ke `master.users.id`) yang berisi role/permissions per company. Semua FK ke user (`user_id`, `created_by`, `acknowledged_by`, dst.) mereferensi ke tabel `user_company_access` di company DB yang sama — **bukan** cross-DB ke master.

```sql
-- User Company Access (LOCAL — registry per-company user + role/permissions)
-- user_id mereferensi master.users.id (NO cross-DB FK)
-- Berisi user khas company ini termasuk admin masing-masing.
-- Role/permissions di-override per company via role_override.
CREATE TABLE user_company_access (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                          -- references master.users.id
    role_override VARCHAR(20),                        -- NULL = pakai global_role dari master.users
    is_active BOOLEAN DEFAULT TRUE,                   -- bisa non-aktif per company
    permissions JSON,                                  -- menu-specific overrides
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY unique_user (user_id),
    INDEX idx_user (user_id),
    INDEX idx_role_override (role_override),
    INDEX idx_active (is_active)
);

-- Vehicles Table (Enterprise-Standard Schema)
-- vehicle_category_code & vehicle_type_code = DENORMALISASI code dari
-- master.vehicle_categories / master.vehicle_types (tanpa FK cross-DB).
-- vehicle_type VARCHAR(50) & driver_name VARCHAR(100) dipertahankan
-- untuk backward compatibility — DEPRECATED, pakai vehicle_type_code &
-- driver_user_id di kode baru.
CREATE TABLE vehicles (
    -- Core identity
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    imei            VARCHAR(30) UNIQUE NOT NULL,
    plate_number    VARCHAR(20) NOT NULL,

    -- Vehicle Identification (enterprise-standard)
    make                 VARCHAR(100) NULL DEFAULT NULL,
    model                VARCHAR(100) NULL DEFAULT NULL,
    variant              VARCHAR(100) NULL DEFAULT NULL,
    year_of_manufacture  YEAR NULL DEFAULT NULL,
    engine_number        VARCHAR(100) NULL DEFAULT NULL,
    chassis_number       VARCHAR(100) NULL DEFAULT NULL COMMENT 'VIN — unique per vehicle',
    color                VARCHAR(50)  NULL DEFAULT NULL,
    fuel_type            ENUM('petrol','diesel','electric','hybrid','CNG','LPG','hydrogen') NULL DEFAULT NULL,

    -- Vehicle Classification (reference master.vehicle_categories / vehicle_types)
    vehicle_category_code VARCHAR(20) NULL DEFAULT NULL,
    vehicle_type_code     VARCHAR(30) NULL DEFAULT NULL,

    -- Registration & Compliance
    registration_number   VARCHAR(50)  NULL DEFAULT NULL,
    registration_expiry   DATE         NULL DEFAULT NULL,
    insurance_number      VARCHAR(100) NULL DEFAULT NULL,
    insurance_expiry      DATE         NULL DEFAULT NULL,
    road_tax_expiry       DATE         NULL DEFAULT NULL,
    inspection_expiry     DATE         NULL DEFAULT NULL,

    -- Physical Specifications (semua dalam mm kecuali yang dinyatakan)
    gross_vehicle_weight  DECIMAL(10,2) NULL DEFAULT NULL,
    payload_capacity      DECIMAL(10,2) NULL DEFAULT NULL,
    vehicle_length        DECIMAL(6,2)  NULL DEFAULT NULL,
    vehicle_width         DECIMAL(6,2)  NULL DEFAULT NULL,
    vehicle_height        DECIMAL(6,2)  NULL DEFAULT NULL,
    wheelbase             DECIMAL(6,2)  NULL DEFAULT NULL,

    -- Driver & Device
    driver_user_id   BIGINT NULL DEFAULT NULL,
    device_model     VARCHAR(100) NULL DEFAULT NULL,
    firmware_version VARCHAR(50)  NULL DEFAULT NULL,

    -- Live State (denormalisasi dari Redis)
    last_seen_at       TIMESTAMP     NULL DEFAULT NULL,
    current_latitude   DECIMAL(10,8) NULL DEFAULT NULL,
    current_longitude  DECIMAL(11,8) NULL DEFAULT NULL,
    current_speed      DECIMAL(5,2)  NULL DEFAULT NULL,

    -- Metadata & Soft Delete
    notes      TEXT NULL,
    status     ENUM('active', 'inactive', 'maintenance') DEFAULT 'active',
    deleted_at TIMESTAMP NULL DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT uq_vehicles_chassis_number UNIQUE (chassis_number),
    INDEX idx_imei                (imei),
    INDEX idx_status              (status),
    INDEX idx_vehicle_type_code   (vehicle_type_code),
    INDEX idx_vehicle_category_code (vehicle_category_code),
    INDEX idx_driver_user_id      (driver_user_id),
    INDEX idx_last_seen_at        (last_seen_at),
    INDEX idx_deleted_at          (deleted_at),
    INDEX idx_make_model          (make, model),
    INDEX idx_current_location    (current_latitude, current_longitude)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- User-Vehicle Mapping (RBAC)
CREATE TABLE user_vehicles (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                         -- references user_company_access.user_id
    vehicle_id BIGINT NOT NULL,
    assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY unique_user_vehicle (user_id, vehicle_id),
    FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    INDEX idx_user (user_id),
    INDEX idx_vehicle (vehicle_id)
);

-- Telemetry Logs (Partitioned per Month)
CREATE TABLE telemetry_logs (
    id BIGINT AUTO_INCREMENT,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,               -- denormalisasi untuk safety
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    speed FLOAT DEFAULT 0,
    heading FLOAT DEFAULT 0,
    altitude FLOAT DEFAULT 0,
    acc_status TINYINT(1) DEFAULT 0,
    battery_level INT DEFAULT 0,
    timestamp DATETIME NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp),
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    INDEX idx_vehicle_time (vehicle_id, timestamp DESC),
    INDEX idx_imei_time (imei, timestamp DESC),
    SPATIAL INDEX idx_location (POINT(longitude, latitude))
) ENGINE=InnoDB
PARTITION BY RANGE (TO_DAYS(timestamp)) (
    PARTITION p_future VALUES LESS THAN MAXVALUE
);
-- Note: monthly partitions added via ALTER TABLE setiap bulan

-- Geofences Table
CREATE TABLE geofences (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    area_type ENUM('circle', 'polygon') NOT NULL,
    coordinates JSON NOT NULL,                       -- GeoJSON
    radius_meters INT,                               -- hanya untuk circle
    boundary_points JSON,                            -- hanya untuk polygon
        created_by BIGINT NOT NULL,                       -- references user_company_access.user_id
    FOREIGN KEY (created_by) REFERENCES user_company_access(user_id),
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_created_by (created_by),
    INDEX idx_active (is_active)
);

-- Geofence-Vehicle Mapping
CREATE TABLE geofence_vehicles (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    geofence_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (geofence_id) REFERENCES geofences(id) ON DELETE CASCADE,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    UNIQUE KEY unique_geofence_vehicle (geofence_id, vehicle_id),
    INDEX idx_geofence (geofence_id),
    INDEX idx_vehicle (vehicle_id)
);

-- Speed Configurations
CREATE TABLE speed_configs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,                       -- NULL = global default
    speed_limit_kmh FLOAT NOT NULL,
    grace_margin_kmh FLOAT DEFAULT 5.0,              -- toleransi sebelum alert
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_active (is_active)
);

-- Alerts Table
CREATE TABLE alerts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    alert_type ENUM('GEOFENCE_BREACH', 'OVERSPEEDING', 'OFFLINE', 'BATTERY_LOW', 'SOS', 'ROUTE_DEVIATION') NOT NULL,
    severity ENUM('low', 'medium', 'high', 'critical') DEFAULT 'medium',
    description TEXT,
    status ENUM('open', 'acknowledged', 'resolved') DEFAULT 'open',
    acknowledged_by BIGINT,                            -- references user_company_access.user_id (NO FK, nullable)
    acknowledged_at TIMESTAMP NULL,
    resolved_at TIMESTAMP NULL,
    vehicle_lat DECIMAL(10, 8),
    vehicle_lon DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    FOREIGN KEY (acknowledged_by) REFERENCES user_company_access(user_id) ON DELETE SET NULL,
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_status (status),
    INDEX idx_alert_type (alert_type),
    INDEX idx_created_at (created_at DESC),
    INDEX idx_severity (severity)
);

-- Notification Preferences
-- Konfigurasi notifikasi per user per tipe alert per channel.
-- Channel: websocket (real-time push), email (SMTP), sms (SMS gateway).
CREATE TABLE notification_preferences (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                           -- references user_company_access.user_id
    FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    alert_type VARCHAR(50) NOT NULL,                    -- e.g. 'geofence', 'speed', 'sos', 'offline', 'battery'
    channel ENUM('websocket', 'email', 'sms', 'push') NOT NULL,
    min_severity ENUM('info', 'warning', 'critical') DEFAULT 'warning',  -- minimum severity untuk channel ini
    delivery_config JSON,                             -- channel-specific config: {"email": "override@corp.com", "phone_number": "+62xxx"} bila berbeda dari profil user
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY unique_user_alert_channel (user_id, alert_type, channel),
    INDEX idx_user (user_id),
    INDEX idx_enabled (is_enabled),
    INDEX idx_severity (min_severity)
);

-- Notifications Table (Sent Notification History)
-- Audit trail untuk semua notifikasi yang dikirim via email/SMS/websocket.
CREATE TABLE notifications (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                          -- references user_company_access.user_id
    alert_id BIGINT,                                  -- nullable; bila terkait alert tertentu
    company_code VARCHAR(20) NOT NULL,                -- denormalisasi untuk query per-tenant
    channel ENUM('websocket', 'email', 'sms', 'push') NOT NULL,
    alert_type VARCHAR(50),                           -- e.g. 'geofence', 'speed', 'sos', 'offline', 'battery'
    subject VARCHAR(255),                             -- email subject / SMS prefix
    body TEXT,                                        -- message body (rendered template)
    status ENUM('pending', 'sent', 'delivered', 'failed', 'skipped') DEFAULT 'pending',
    provider_response JSON,                           -- response dari provider (SMS gateway / SMTP server)
    error_message TEXT,                               -- error bila gagal
    retry_count INT DEFAULT 0,
    sent_at TIMESTAMP NULL,                           -- ketika berhasil dikirim
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_user (user_id),
    INDEX idx_alert (alert_id),
    INDEX idx_company (company_code),
    INDEX idx_status (status),
    INDEX idx_created_at (created_at DESC),
    INDEX idx_channel_status (channel, status)
);

-- Routes Table
CREATE TABLE routes (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    waypoints JSON NOT NULL,                           -- array of {lat, lon}
    estimated_duration_sec INT,
    created_by BIGINT NOT NULL,                         -- references user_company_access.user_id
    FOREIGN KEY (created_by) REFERENCES user_company_access(user_id)
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_active (is_active),
    INDEX idx_created_by (created_by)
);

-- Route Assignments
CREATE TABLE route_assignments (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    route_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    driver_user_id BIGINT NOT NULL,                     -- references user_company_access.user_id
    FOREIGN KEY (driver_user_id) REFERENCES user_company_access(user_id)
    status ENUM('not_started', 'in_progress', 'completed', 'delayed') DEFAULT 'not_started',
    started_at TIMESTAMP NULL,
    completed_at TIMESTAMP NULL,
    deviation_meters FLOAT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (route_id) REFERENCES routes(id),
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    INDEX idx_route (route_id),
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_status (status),
    INDEX idx_driver (driver_user_id)
);

-- ============================================================================
-- Fuel Logs (v1.3.0 — Modul 7, FR-7.4). Ter-partisi bulanan TO_DAYS(timestamp)
-- dengan pola telemetry_logs: TANPA FOREIGN KEY & tanpa spatial index
-- (keterbatasan MySQL 8.0 pada tabel ter-partisi — lihat catatan deviasi §6.2).
-- Semua pembacaan sensor bahan bakar masuk di sini, termasuk yang fuel-only
-- (tanpa GPS); baris telemetry ber-posisi tetap ke telemetry_logs apa adanya.
-- ============================================================================
CREATE TABLE fuel_logs (
    id           BIGINT AUTO_INCREMENT,
    vehicle_id   BIGINT NOT NULL,
    imei         VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,               -- denormalisasi untuk safety
    fuel_level   DECIMAL(6,2) NULL,                  -- persen 0–100 (atau raw pra-kalibrasi)
    fuel_volume  DECIMAL(10,3) NULL,                 -- liter (bila tersedia/kalibrasi)
    fuel_temp_c  DECIMAL(5,2) NULL,                  -- suhu sensor °C (opsional)
    acc_status   TINYINT(1) DEFAULT 0,
    timestamp    DATETIME NOT NULL,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp),
    INDEX idx_fuel_vehicle_time (vehicle_id, timestamp DESC),
    INDEX idx_fuel_imei_time (imei, timestamp DESC)
) ENGINE=InnoDB
PARTITION BY RANGE (TO_DAYS(timestamp)) (
    PARTITION p_2025_Q4 VALUES LESS THAN (TO_DAYS('2026-01-01')),
    PARTITION p_2026_Q1 VALUES LESS THAN (TO_DAYS('2026-04-01')),
    PARTITION p_2026_Q2 VALUES LESS THAN (TO_DAYS('2026-07-01')),
    PARTITION p_2026_Q3 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p_2026_Q4 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);

-- Fuel Configs (v1.3.0 — Modul 7, FR-7.6): threshold alert per-vehicle/global.
-- vehicle_id NULL = default global (pola speed_configs: vehicle-specific menang).
CREATE TABLE fuel_configs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NULL,
    drop_threshold_percent   DECIMAL(5,2) NOT NULL DEFAULT 15.00,  -- FUEL_DROP bila turun > ini dlm window
    window_seconds           INT          NOT NULL DEFAULT 300,
    refuel_threshold_percent DECIMAL(5,2) NOT NULL DEFAULT 10.00,  -- REFUEL bila naik > ini dlm window
    alert_enabled            BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_fuel_vehicle (vehicle_id)
);

-- Media Events (v1.3.0 — Modul 8, FR-8.3): katalog media dashcam per company DB.
CREATE TABLE media_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,               -- denormalisasi untuk safety
    media_type ENUM('photo','video_clip') NOT NULL,
    trigger_type ENUM('sos','alarm','geofence','overspeed','manual','scheduled','power') NOT NULL,
    object_key VARCHAR(255) NOT NULL,                -- {company}/{vehicle}/{yyyyMM}/{uuid}
    bucket VARCHAR(63) NOT NULL,
    size_bytes BIGINT DEFAULT 0,
    duration_seconds INT NULL,                       -- video saja
    mime_type VARCHAR(64) NOT NULL,
    sha256 CHAR(64) NULL,
    status ENUM('uploaded','available','expired','failed') DEFAULT 'uploaded',
    taken_at DATETIME NOT NULL,                      -- waktu event di device
    meta JSON NULL,                                  -- metadata vendor tambahan
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,                        -- soft-delete (FR-8.4)
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    INDEX idx_media_vehicle_taken (vehicle_id, taken_at DESC),
    INDEX idx_media_trigger (trigger_type, taken_at DESC),
    INDEX idx_media_status (status)
);


## 7. Configuration Management (NEW SECTION)

### 7.1 Environment Variables

### 7.1.1 Database Provider Fleksibel (`DATABASE_PROVIDER`) — NEW 2026-08-25

Penyimpanan persisten dapat **dipilih per deployment** lewat satu variabel di
`backend/.env` (single source of truth untuk seluruh service & docker-compose).
Provider lain tidak pernah dihapus — kapan pun bisa dipilih kembali.

```dotenv
DATABASE_PROVIDER=postgres   # postgres (default proyek) | mysql
COMPOSE_PROFILES=postgres    # kopel compose; backend/scripts/compose-up.sh mengisinya otomatis
```

| Aspek | `mysql` | `postgres` — **default proyek** |
|---|---|---|
| Driver `database/sql` | `mysql` (go-sql-driver) | `pgx` v5 stdlib (`sql.Open("pgx", …)`; placeholder `?` otomatis menjadi `$N`) |
| Koneksi app langsung | `MYSQL_HOST/PORT/USER/PASSWORD/DB` | `POSTGRES_*` (atau `DATABASE_URL`, prioritas tertinggi) |
| Multi-tenant | 1 database fisik per tenant: `adatrack_gps_{code}` | 1 database fisik (`POSTGRES_DB`) + **schema** per tenant via `search_path`: `adatrack_gps_master`, `adatrack_gps_{code}` |
| Migrasi master/company | `database/migrations/master/`, `…/company/` | `database/migrations/master_pg/`, `…/company_pg/` (dipilih otomatis oleh `tenant.Config.MigrationsDirFor()`) |
| Bootstrap schema dev | `database/init/00-multitenant-init.sh` (+ apply `seed/reference/`) | `database/init-pg/01_schemas→04-replicator` incl. **`02b-seed-reference.sh`** (apply `seed/reference/` dgn transform ODKU→ON CONFLICT; butuh mount `./database/seed:/db/seed`) |
| Pool sizing | `MYSQL_POOL_MIN/MAX` | `POSTGRES_POOL_MIN/MAX` |

Satu-satunya titik cabang engine adalah paket **`internal/dialect`** (driver
name, quoting identifier, INSERT … ON DUPLICATE KEY vs ON CONFLICT,
RETURNING id, split statement migrasi); seluruh service Go berbagi jalur kode
yang sama.

**Perilaku docker-compose — tidak semua DB ikut up.** Service `mysql` dan
`postgres` di `backend/docker-compose.yml` masing-masing diberi *compose
profile* bernama sama dengan provider-nya (`profiles: ["mysql"]` /
`profiles: ["postgres"]`); Redis & NATS selalu up. Cara menjalankan:

1. **Disarankan (anti-salah-set):** `backend/scripts/compose-up.sh up -d` —
   skrip membaca `DATABASE_PROVIDER` dari `backend/.env` (OS env menang),
   memvalidasi nilainya (nilai lain → gagal cepat, tidak ada DB yang nyasar
   up), lalu men-set `COMPOSE_PROFILES` otomatis.
2. **Manual:** isi `COMPOSE_PROFILES=<sama dengan DATABASE_PROVIDER>` di
   `backend/.env`, lalu `cd backend && docker compose up -d`.
3. **Entry point deployments:** `docker compose -f backend/deployments/docker-compose.yml --env-file backend/.env up -d`.

Aturan main saat berganti provider:
- `docker compose down` dulu → ubah `DATABASE_PROVIDER` → provisikan schema
  provider tujuan (`database/init/` atau `database/init-pg/`). **Data TIDAK
  berpindah otomatis** antar engine (tidak ada data-migration lintas-provider
  di fase ini).
- Container provider lama yang terlanjur dibuat tidak ter-cover `down` aktif
  (profil berbeda); bereskan manual bila perlu: `docker compose --profile mysql down`.
- Monitoring: `mysqld-exporter` hanya ikut up pada profile `mysql`; saat
  provider `postgres`, target metrik `mysql_*` memang down (normal) —
  postgres_exporter adalah work-on-later. Overlay replika
  (`deployments/docker-compose.ha.yml`) mengikuti provider aktif — hanya
  replika engine terpilih yang dibuat.

#### Replika READ (2026-08-25): replica = baca, primary = tulis

Replika database **bukan untuk backup dan bukan failover** — fungsinya READ
SCALING: query baca (laporan/analitik/riwayat) diarahkan ke replika, tulisan
tetap ke primary:

- `mysql-replica` — GTID, `read_only=ON`; `postgres-replica` — streaming WAL +
  physical slot `pg_replica_slot` (standby read-only); `redis-replica` —
  `replicaof` master (**pengecualian sesuai HA doc §3/§5**: berfungsi sebagai
  cadangan failover-cepat state ≤5 mnt — bukan read-scaling). Semuanya
  ber-profile mengikuti `DATABASE_PROVIDER`.
- Proteksi data TETAP lewat backup harian (`backend/scripts/backup-mysql.sh`)
  + uji restore (drill sudah lulus).
- Skrip promote **database** `promote-mysql-replica.sh` /
  `promote-postgres-replica.sh` DIHAPUS (2026-08-25). Yang tersisa:
  `setup-*-replication.sh` + `replication-status.sh`; **Redis mempertahankan**
  `promote-redis-replica.sh` + drill `drill-redis-failover.sh` (LIVE ✓).
- **PostgreSQL streaming LIVE terverifikasi 2026-08-25**: `walreceiver |
  streaming`, slot `active=true wal_status=reserved`, INSERT di primary muncul
  di replika (PROPAGATION_OK), INSERT ke replika ditolak (READONLY_OK).
- **Read/Write split APP-LEVEL LIVE kedua engine (2026-08-25, fase B4):**
  `internal/tenant` menyediakan pool REPLICA per-tenant + `ReadPool()` /
  `ReadRouter` (baca → replika, fallback satu-kali ke primary; tulis SELALU
  primary) dan dipakai service-websocket, api-vehicle, worker-alert. Terveri-
  fikasi via `backend/cmd/db-replica-probe`:
    - PostgreSQL : READ→REPLICA (`pg_is_in_recovery=true`), WRITE→PRIMARY,
      metrik `db_read_queries_total{route=replica}=2 primary=0`.
    - MySQL      : diuji KHUSUS pada sesi B4 ini dengan mysql + mysql-replica
      di-up sementara (env aktif tetap `postgres`) — GTID primary ditambahkan
      di compose, replikasi dual-Yes, probe LOLOS (READ→replika :3407 lewat
      cek `@@read_only`, WRITE→primary :3307); **kedua container MySQL langsung
      DI-DOWN setelah pengujian** (volume dipertahankan). Rincian: HA doc §6b.

**Batasan (jujur):** fondasi provider-aware (config → tenant → migrasi → batch
insert persistence) sudah teruji; namun sebagian SQL controllers REST
(`api-vehicle`, `service-websocket`) dan repos `worker-alert` masih
MySQL-specific (backtick, `JSON_OBJECT`, dsb.) — porting penuh tercatat di
`docs/POSTGRES_PROVIDER.md §6`.

> **Mode PostgreSQL (default proyek):** blok env per-service di bawah mencatat variabel
> **MySQL/legacy** (`MASTER_DB_*`, `MYSQL_POOL_*`) yang tetap dipakai saat
> `DATABASE_PROVIDER=mysql`. Untuk `DATABASE_PROVIDER=postgres`, koneksi DB diarahkan oleh
> **`DATABASE_URL`** (prioritas tertinggi) atau kombinasi **`POSTGRES_HOST/PORT/DB/USER/PASSWORD`**,
> `POSTGRES_POOL_MIN/MAX`, dan `COMPANY_DB_PREFIX=adatrack_gps_` (schema per tenant via
> `search_path`). Kedua setenv (MySQL & postgres) sudah tersedia di `backend/.env`. Nomor port
> contoh: PostgreSQL primary `5532` (dev compose) / native `5432`, MySQL `3306`.
> Kedua varian berbagi `NATS_*`, `REDIS_*`, `JWT_*`, `LOG_LEVEL`, dsb.


**For ingestion-tcp:**
```bash
TCP_PORT=9000
TCP_MAX_CONNECTIONS=5000
# Dedicated per-protocol listeners ("0" = disabled).
TELTONIKA_TCP_PORT=9001
TK103_TCP_PORT=9002
# GT06 date encoding: plain-hex (default, sesuai docs Concox) atau BCD (beberapa firmware).
GT06_DATE_BCD=false
NATS_URL=nats://localhost:4222
NATS_SUBJECT_PREFIX=telemetry
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth (users) + IMEI→company_code lookup
LOG_LEVEL=info
```

**For worker-persistence:**
```bash
NATS_URL=nats://localhost:4222
NATS_SUBJECT=telemetry.raw.>
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth + IMEI→company_code lookup
COMPANY_DB_PREFIX=adatrack_gps_          # format: adatrack_gps_{LOWERCASE(COMPANY_CODE)} (e.g. ABLE01 → adatrack_gps_able01)
MYSQL_POOL_MIN=20
MYSQL_POOL_MAX=50
BATCH_SIZE=500
BATCH_TIMEOUT_SEC=5
RETRY_MAX=3
RETRY_BACKOFF_MS=1000,5000,10000
LOG_LEVEL=info
```

**For worker-live:**
```bash
NATS_URL=nats://localhost:4222
NATS_SUBJECT=telemetry.raw.>
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth + IMEI→company_code lookup
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_KEY_PREFIX=adatrack_gps_          # format: adatrack_gps:{company_code}:vehicle:state:<IMEI> (company_code lowercase)
REDIS_POOL_MIN=10
REDIS_POOL_MAX=30
REDIS_TTL_SEC=300
BATCH_UPDATE_INTERVAL_MS=100
LOG_LEVEL=info
```

**For service-websocket:**
```bash
HTTP_PORT=8080
HTTP_TLS_CERT=
HTTP_TLS_KEY=
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth + IMEI→company_code lookup
COMPANY_DB_PREFIX=adatrack_gps_          # format: adatrack_gps_{LOWERCASE(COMPANY_CODE)} (e.g. ABLE01 → adatrack_gps_able01)
COMPANY_MIGRATIONS_DIR=./backend/database/migrations/company  # migration SQL dir for auto-provision (B2/B3)
MYSQL_POOL_MIN=20
MYSQL_POOL_MAX=50
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_KEY_PREFIX=adatrack_gps_          # format: adatrack_gps:{company_code}:vehicle:state:<IMEI> (company_code lowercase)
NATS_URL=nats://localhost:4222
JWT_SECRET=your-secret-key
JWT_EXPIRY_HOURS=24
WS_MAX_CONNECTIONS=5000
WS_SEND_BUFFER_KB=256
WS_MAX_QUEUE=1000
LOG_LEVEL=info
```

**For api-vehicle:**
```bash
HTTP_PORT=8081
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth + IMEI→company_code lookup
COMPANY_DB_PREFIX=adatrack_gps_          # format: adatrack_gps_{LOWERCASE(COMPANY_CODE)} (e.g. ABLE01 → adatrack_gps_able01)
MYSQL_POOL_MIN=20
MYSQL_POOL_MAX=50
JWT_SECRET=your-secret-key
JWT_EXPIRY_HOURS=24
LOG_LEVEL=info
```

**For worker-alert:**
```bash
NATS_URL=nats://localhost:4222
NATS_SUBJECT=telemetry.raw.>
MASTER_DB_HOST=localhost
MASTER_DB_PORT=3306
MASTER_DB_USER=adatrack_gps_user
MASTER_DB_PASSWORD=secret
MASTER_DB_NAME=adatrack_gps_master    # auth + IMEI→company_code lookup
COMPANY_DB_PREFIX=adatrack_gps_          # format: adatrack_gps_{LOWERCASE(COMPANY_CODE)} (e.g. ABLE01 → adatrack_gps_able01)
REDIS_HOST=localhost
REDIS_PORT=6379
REDIS_KEY_PREFIX=adatrack_gps_          # format: adatrack_gps:{company_code}:vehicle:state:<IMEI> (company_code lowercase)
LOG_LEVEL=info
# --- Notification Delivery (Email + SMS) ---
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=notifications@adatrackgps.io
SMTP_PASSWORD=secret-smtp-password
SMTP_FROM=notifications@adatrackgps.io
SMTP_TLS=true
SMS_PROVIDER=twilio                     # opsional: "twilio", "aws_sns", "none"
SMS_TWILIO_ACCOUNT_SID=ACxxxx
SMS_TWILIO_AUTH_TOKEN=your-twilio-token
SMS_FROM=+1234567890                    # nomor WhatsApp/SMS resmi perusahaan
NOTIFICATION_RETRY_MAX=3                # max retry bila gagal kirim email/SMS
NOTIFICATION_RETRY_BACKOFF_MS=2000,6000,12000  # exponential backoff
NOTIFICATION_BATCH_INTERVAL_MS=100       # batch notifikasi tiap 100ms
NOTIFICATION_RATE_LIMIT=100              # max notifikasi per company per detik (anti-spam)
# --- Fuel Sensor (v1.3.0 — Modul 7) ---
TELTONIKA_IO_FUEL_LEVEL=86               # daftar AVL IO ID (csv) → fuel_level (%)
TELTONIKA_IO_FUEL_USED=                  # csv AVL IO ID → fuel terpakai (L), opsional
TELTONIKA_IO_FUEL_TEMP=                  # csv AVL IO ID → suhu BBM (°C), opsional
FUEL_DROP_THRESHOLD_PERCENT=15           # fallback global bila fuel_configs kosong
FUEL_WINDOW_SECONDS=300                  # window deteksi drop/refuel
FUEL_REFUEL_THRESHOLD_PERCENT=10         # fallback global REFUEL
# --- Dashcam Event Media (v1.3.0 — Modul 8, service-media) ---
SERVICE_MEDIA_HTTP_ADDR=:8095            # port REST service-media
MEDIA_S3_ENDPOINT=http://localhost:9000  # MinIO dev; prod = AWS S3/OSS endpoint
MEDIA_S3_BUCKET=adatrack-media
MEDIA_S3_ACCESS_KEY=minioadmin
MEDIA_S3_SECRET_KEY=minioadmin
MEDIA_S3_USE_SSL=false
MEDIA_PRESIGN_TTL_SECONDS=600            # TTL presigned URL GET/PUT
MEDIA_MAX_FILE_MB=100                    # fallback global bila company_media_config kosong
MEDIA_CLEANUP_CRON=0 3 * * *             # job retensi harian (FR-8.7)
```


---

## 8. Monitoring & Observability (NEW SECTION)

### 8.1 Prometheus Metrics

**ingestion-tcp:**
- `tcp_connections_active` - Current active TCP connections
- `tcp_connections_total` - Total connections since startup
- `tcp_parse_errors_total` - Number of parsing errors
- `tenant_resolution_duration_ms` - IMEI → company_code lookup latency (master DB)
- `tenant_lookup_errors_total` - Failed IMEI-to-company lookups (unregistered devices)
- `nats_publish_duration_ms` - NATS publish latency
- `nats_publish_errors_total` - NATS publish failures
- `backpressure_drops_total` - Messages dropped due to backpressure
- **Tenant-specific:** `company_code` label on publish metrics per company

**worker-persistence:**
- `batch_insert_duration_ms` - Time to insert batch to DB engine (PostgreSQL default / MySQL)
- `batch_insert_size` - Number of records per batch
- `batch_insert_errors_total` - Failed batch inserts
- `db_pool_connections_active` - Active DB connections **per company DB/schema**
- `company_db_pool_count` - Number of active company database connection pools
- `tenant_routing_duration_ms` - company_code → DB pool resolution latency
- `retry_attempts_total` - Number of retry attempts
- `messages_processed_total` - Total messages processed (label: `company_code`)

**worker-live:**
- `redis_update_duration_ms` - Redis write latency
- `redis_batch_size` - Records per batch update
- `redis_errors_total` - Redis operation failures
- `redis_pool_connections_active` - Active Redis connections
- `vehicle_state_updates_total` - Total state updates

**service-websocket:**
- `ws_connections_active` - Current WebSocket connections
- `ws_connections_total` - Total connections since startup
- `ws_broadcast_duration_ms` - Time to broadcast message to all subscribers
- `ws_message_queue_size` - Messages pending per connection
- `http_request_duration_ms` - REST API latency
- `http_errors_total` - HTTP request failures
- `rbac_check_duration_ms` - Permission check latency
- `tenant_db_connections_active` - Active connections per company DB (label: `company_code`)
- `tenant_routing_duration_ms` - company_code → DB pool resolution latency on REST requests

**worker-alert:**
- `alerts_generated_total{alert_type, severity}` - Total alerts created (label: `alert_type`, `severity`)
- `alerts_processed_total{company_code}` - Alerts processed per company (label: `company_code`)
- `geofence_breach_duration_ms` - Geofence breach detection latency
- `overspeed_detected_total` - Total speed violations detected
- `sos_events_total` - SOS events detected (critical)
- `notifications_sent_total{channel}` - Total notifications sent (label: `channel`)
- `notifications_pending_total` - Notifications queued for delivery
- `notification_delivery_duration_ms{channel}` - Delivery latency per channel (email/sms)
- `notification_errors_total{channel}` - Delivery errors per channel
- `notification_retry_total` - Number of retry attempts for failed notifications
- `sms_provider_balance` - Remaining SMS provider balance
- `notification_deadletter_total` - Notifications that exhausted retries (DLQ)

**Fuel sensor (B5a, v1.3.0 — Modul 7):**
- `fuel_readings_total{protocol}` - Paket fuel sensor berhasil diparse per protokol
- `fuel_rows_inserted_total` - Baris yang ditulis ke `fuel_logs`
- `fuel_rows_positionless_total` - Pembacaan fuel tanpa GPS (tidak masuk telemetry_logs)
- `fuel_alerts_total{alert_type}` - Alert FUEL_DROP / REFUEL yang tercipta

**service-media (B5b, v1.3.0 — Modul 8):**
- `media_uploads_total{company_code, media_type}` - Media ter-ingest per company/tipe
- `media_upload_bytes_total{company_code}` - Total byte tersimpan ke object storage
- `media_presigned_total{company_code}` - Jumlah presigned URL diterbitkan
- `media_cleanup_deleted_total` - Objek dihapus job retensi harian
- `storage_objects{bucket}` - Gauge jumlah objek per bucket
- `http_request_duration_ms` / `http_errors_total` - Latensi/error REST service-media

### 8.2 Health Checks

**Readiness Probes (before accepting traffic):**
- TCP connection to master database ✓
- TCP connection to all active company databases ✓
- Redis connectivity ✓
- NATS connectivity ✓
- All required environment variables set ✓
- Tenant resolution cache initialized (IMEI → company_code) ✓

**Liveness Probes (while running):**
- Master DB query response time < 5 seconds ✓
- Company DB pool count matches registered companies ✓
- NATS connection stable ✓
- Worker process running without deadlock ✓
- Tenant resolution cache hit ratio > 95% ✓

### 8.3 Alerting Rules

| Condition | Severity | Action |
|-----------|----------|--------|
| TCP connections > 90% of limit (4500) | WARNING | Scale up ingestion layer |
| NATS pending messages > 90% of max (9000) | CRITICAL | Page on-call, check persistence layer |
| DB insert latency > 10 sec | CRITICAL | Check DB (PostgreSQL/MySQL), may need query optimization |
| **Tenant resolution latency > 500ms** | CRITICAL | Master DB slow; check pool/connection |
| **Company DB pool exhaustion (>90% max)** | CRITICAL | Scale DB connections or investigate stuck pool |
| **Tenant lookup error rate > 5%** | WARNING | Unregistered devices flooding; check allowlist |
| WebSocket broadcast latency > 1 sec | WARNING | Check network, may drop users |
| Error rate > 5% | WARNING | Investigate root cause |
| System uptime < 99.9% | CRITICAL | Post-mortem analysis |
| **Notification delivery failure rate > 5%** | CRITICAL | Check SMTP/SMS gateway connectivity, verify credentials |
| **Notification retry exhausted > 10/min** | WARNING | Provider rate limit; check SMS/email quotas |
| **SMS provider balance < 100** | WARNING | Top up provider account |

## 8.4 Notification Delivery

**Delivery mechanism:** `worker-alert` berlangganan `alert.{type}.{company_code}.*` dari NATS, lookup `notification_preferences` di company DB, dan kirim notifikasi via channel yang dikonfigurasi per user.

**Email delivery (SMTP):**
- Pakai `NET SMTP` library (Golang). TLS/SSL mandatory.
- Template engine: `text/template` + `html/template` — template per alert type (geofence_entry, geofence_exit, speed, sos, offline, battery).
- Template path: `backend/services/worker-alert/templates/{alert_type}_{channel}.tmpl`
- Rate limit: `NOTIFICATION_RATE_LIMIT` per company untuk mencegah spam.

**SMS delivery (gateway):**
- Provider yang didukung: **Twilio** (default dev), **AWS SNS** (prod). Pilih via `SMS_PROVIDER`.
- Pakai SDK masing-masing provider. Fallback ke email bila SMS gagal berulang.
- Format nomor: E.164 (+62812xxxxxx). Nomor diambil dari `user_company_access` (lihat §6.2 users table) atau `delivery_config.phone_number` override.
- `SMS_FROM` = nomor resmi perusahaan (ter-config di env).

**Retry & Dead Letter Queue:**
- Retry: exponential backoff (`NOTIFICATION_RETRY_BACKOFF_MS`), max `NOTIFICATION_RETRY_MAX` percobaan.
- Bila retry habis: status `failed` di `notifications` table, publish ke NATS `notification.deadletter.{company_code}` untuk manual handling.
- `notifications.status` tracking: `pending → sent → delivered → failed → skipped`.
- `provider_response JSON` menyimpan response mentah dari provider untuk debugging.

**Notification templates:**
| Alert Type | Email Template | SMS Template |
|---|---|---|
| geofence_entry | `templates/email/geofence_entry.tmpl` | `templates/sms/geofence.txt` |
| geofence_exit | `templates/email/geofence_exit.tmpl` | `templates/sms/geofence.txt` |
| speed | `templates/email/speed.tmpl` | `templates/sms/speed.txt` |
| sos | `templates/email/sos_critical.tmpl` | `templates/sms/sos.txt` |
| offline | `templates/email/offline.tmpl` | `templates/sms/offline.txt` |
| battery | `templates/email/battery.tmpl` | `templates/sms/battery.txt` |

**Metrics (PRD §8.1 tambahan untuk notifikasi):**
- `notifications_total{channel, status}` — counter per channel & status
- `notification_delivery_duration_ms{channel}` — latency kirim
- `notification_errors_total{channel}` — error counter
- `notification_retry_total` — jumlah retry yang dilakukan
- `sms_provider_balance` — sisa balance SMS (Twilio/AWS SNS)
- `notification_deadletter_total` — notifikasi yang masuk dead letter queue

---

## 9. Implementation Phases

### Phase 1: Data Persistence (Week 1-2)
**Goal:** Ensure data durability, prevent data loss

- ⚠️ **Implement worker-persistence** with:
  - Batch insert logic (500 records / 5 sec)
  - DB connection pooling (20-50 conn; `POSTGRES_POOL_*` default / `MYSQL_POOL_*` selectable)
  - Error handling + retries
  - Metrics collection
  - Load testing: 1000 msg/sec sustained

### Phase 2: Real-Time API + WebSocket (Week 2-3)
**Goal:** Enable live dashboard + historical queries

- ⚠️ **Implement service-websocket** with:
  - REST API endpoints (vehicles, history, geofences)
  - WebSocket server with RBAC filtering
  - User authentication + authorization
  - Connection pooling
  - Load testing: 1000 concurrent users + 5000 devices

### Phase 3: Performance Optimization (Week 4+)
**Goal:** Optimize for scale, reduce latency

- Add database indices + materialized views
- Optimize WebSocket broadcast mechanism
- Redis clustering (if needed)
- Add comprehensive monitoring + alerting
- Capacity planning + scaling procedures

### Phase 4: Fuel Sensor & Dashcam Event Media (v1.3.0 — B5a/B5b)
**Goal:** Kanal telemetri bahan bakar end-to-end + pipeline media peristiwa dashcam (scope A)

- **B5a — Fuel Sensor (Modul 7):** parse GT06 `0x0D` + mapping IO Teltonika → tabel
  `fuel_logs` (partitioned) → merge live-state Redis → alert FUEL_DROP/REFUEL
  (`alert.fuel.<company>`) → API fuel history & fuel-configs.
- **B5b — Dashcam Event Media (Modul 8):** service baru `service-media` (Gin) + paket shared
  `internal/storage` (MinIO/S3) + tabel `media_events`/`company_media_config` + ingest HMAC +
  RBAC API + presigned URL + WS `MEDIA_EVENT` via `media.event.<company>` + job retensi harian.
- Detail task & acceptance criteria: `.clinerules/03-backend-phases.md` Phase B5a / B5b.

---

## 10. Non-Functional Requirements

| Requirement | Target | Verification |
|---|---|---|
| **Latency** | < 800 ms end-to-end | Performance test with 1000 devices |
| **Throughput** | 400 msg/sec peak (≤1,000 devices) | Load test with spike traffic |
| **Uptime** | 99.9% | Monitor for 30 days |
| **Data Loss** | Zero tolerated | Audit trail + recovery procedures |
| **Database Query** | < 1.5 sec (30-day history) | Benchmark with real data |
| **WebSocket Broadcast** | < 500ms per 1000 users | Performance test with concurrent subscribers |
| **Tenant Isolation** | 100% enforced | Verify no cross-tenant data access in tests |
| **Code Quality** | 80%+ test coverage | Unit + integration tests |
| **Documentation** | Complete API + deployment guide | Auto-generated from code |

---

## 11. Risk Assessment & Mitigation

| Risk | Impact | Mitigation |
|---|---|---|
| **Peak Traffic Spike** | HIGH | NATS queue + backpressure logic + worker auto-scaling |
| **Database Lock Contention** | HIGH | Batch inserts + partitioning + connection pooling |
| **Tenant Isolation Breach** | HIGH | JWT company_code validation + DB routing check on every request |
| **Tenant Resolution Failure** | MEDIUM | Master DB lookup fallback + retry + cache; reject unregistered IMEI |
| **Redis Memory Overflow** | MEDIUM | TTL + max eviction policy + monitoring |
| **WebSocket Connection Drop** | MEDIUM | Graceful reconnect + client-side retry logic |
| **GPS Device Disconnection** | LOW | TCP keep-alive + heartbeat monitoring |
| **Data Retention Growth** | MEDIUM | Partitioning + data purging + archival strategy (per company DB) |

---

## 12. Success Criteria

✅ All 6 backend services implemented (multi-tenant: **PostgreSQL** schema-per-tenant default; **MySQL** database-per-tenant selectable; master + ≤50 company schemas/DBs)
✅ Sustained 400 msg/sec without data loss (≤1,000 devices across all companies)
✅ Dashboard displays real-time locations for all authorized vehicles (tenant-isolated)
✅ Query 30-day history in < 1.5 seconds (per company DB, partitioned)
✅ 99.9% uptime achieved in production (2-server HA: PRIMARY + STANDBY)
✅ Zero cross-tenant data access (company isolation verified)
✅ Comprehensive monitoring + alerting active (incl. tenant resolution & DB pool metrics)
✅ Fuel sensor end-to-end (v1.3.0/B5a): pembacaan tersimpan di `fuel_logs`, alert FUEL_DROP/REFUEL terpicu & ter-notifikasi
✅ Dashcam event media (v1.3.0/B5b): ingest HMAC → MinIO/S3 → katalog `media_events` ber-RBAC → presigned URL → WS `MEDIA_EVENT` → retensi per company

---

## Lampiran A — Gap Tracking (Catatan Kelengkapan Dokumen)

> Bagian ini menutup seluruh "catatan/kelengkapan" yang selama ini hanya tersedia terfragmentasi:
> tiap entri gap dinyatakan **RESOLVED** dan dijelaskan **bagaimana** hal itu terwujud pada
> spesifikasi & implementasi, sehingga dokumen ini lengkap dan berdiri sendiri.

| No | Gap (topik kelengkapan) | Status | Penyelesaian (intrinsik pada dokumen ini) |
|---|---|---|---|
| 1 | Kontrak respons REST & event WebSocket | ✅ RESOLVED | Di §4 Modul 5 — format `VEHICLE_UPDATE`/event WS; format response standar `{ status, data, pagination }`; format error GAP #3. |
| 2 | Autentikasi & keamanan | ✅ RESOLVED | §5.5 skema master `users` (email + bcrypt); JWT HS256 + refresh/rotation/revocation (tokenauth); RBAC; rate-limit login 5/15mnt + lockout. |
| 3 | Penanganan error & kode HTTP | ✅ RESOLVED | Format error `{ status, error_code, message, timestamp }`; kode 200/201/400/401/403/404/429/500/503 (di seluruh API service-websocket & api-vehicle). |
| 4 | Retensi & arsip data | ✅ RESOLVED | Partitioning `telemetry_logs`/`fuel_logs`; job retensi media (FR-8.7); backup/restore scripts. |
| 5 | Disaster recovery | ✅ RESOLVED | `scripts/backup-mysql.sh`/`restore-mysql.sh`/`backup-redis.sh`; drill restore nyata lulus; HA 2-server. |
| 6 | Load / endurance testing | ✅ RESOLVED | `backend/loadtest-suite.sh`; baseline 400 & peak 2000 msg/s → **0 data loss**. |
| 7 | Deployment & DevOps | ✅ RESOLVED | Dockerfile 6 service; compose (bootstrap/provider-aware); `/healthz`+`/metrics`. |
| 8 | Detail protokol TCP | ✅ RESOLVED | §4 Modul 1/1a/1b (GT06/Concox, Teltonika, TK103; framing, CRC-ITU, anti-spoofing). |
| 9 | Konvensi subjek NATS | ✅ RESOLVED | §4 Modul 4 (subject `telemetry.*`/`alert.*`/`notify.*`/`media.*` + queue groups). |
| 10 | Kepatuhan & privasi (compliance) | ✅ RESOLVED | Database-per-tenant; RBAC row-level; audit `notifications`+`audit`; tenant-isolation 403. |
| 11 | Strategi pengujian | ✅ RESOLVED | Unit test + E2E per service; target coverage ≥80%. |
| 12 | Checklist keamanan | ✅ RESOLVED | JWT refresh/revocation, bcrypt cost 12, parameterized SQL, CORS & security headers, rate limit, WebSocket origin. |
| 13 | **Sensor bahan bakar** (enterprise) | ✅ RESOLVED | **Modul 7 (FR-7.1–7.8)** — fase **B5a**. |
| 14 | **Dashcam / media pipeline** | ✅ RESOLVED | **Modul 8 (FR-8.1–8.8, scope A)** — fase **B5b**; live streaming out-of-scope. |

**Skor kelengkapan:** sebelum resolusi ini kelengkapan dokumen dinilai ~50% (kebutuhan fungsional
lengkap, detail operasional belum). Setelah v1.3.0 semua 14 catatan di atas terpecah/konsolidasi,
sehingga dokumen ini **berdiri sendiri dan lengkap**.

---
---

## Lampiran B — Feature Coverage (Cakupan Fitur)

> Peta cakupan tiap kelompok fitur terhadap modul/persyaratan (FR) & fase implementasi,
> disajikan diri-sendiri agar tidak perlu merujuk dokumen lain. Semua fitur inti produk —
> termasuk yang dulunya hanya "minimal" — kini sudah didisposisikan ke FR & fase.

| Fitur | Status historis | Spesifikasi ini (FR) | Fase implementasi | Status implementasi |
|---|---|---|---|---|
| **GEOFENCE** | ⚠️ partial → ✅ | FR-4.4 / §4 Modul 3 (circle+polygon, ray-casting, Haversine), state Redis, `geofence_vehicles` | B3 (worker-alert) + B2/B3 (API CRUD) | ✅ implementasi & E2E live terverifikasi |
| **ROUTE / NAVIGATION** | ❌ minimal → ✅ | FR-4.8 (`routes`, `route_assignments`, deviasi `ROUTE_DEVIATION`, `routes/:id/track`) | B3 | ✅ implementasi & E2E live terverifikasi |
| **HISTORY PLAYBACK** | ⚠️ partial → ✅ | FR-5.3 (`GET /vehicles/{id}/history`, partitioned) | B3/B4 | ✅ query < 1.5 s (verified via `cmd/querybench`) |
| **ALERTS (semua tipe)** | ⚠️ → ✅ | FR-4.4–4.7 (GEOFENCE_BREACH, OVERSPEEDING, BATTERY_LOW, OFFLINE, SOS, ROUTE_DEVIATION, FUEL_DROP/REFUEL) | B3 (+B5a fuel) | ✅ implementasi & E2E live terverifikasi (SOS +eskalasi/TTA) |
| **NOTIFIKASI** | ⚠️ → ✅ | FR-4.1.5 (`notification_preferences` per user/tipe/channel/min_severity; WebSocket/email/SMS) | B3 | ✅ |
| **SOS** | ⚠️ → ✅ | FR-4.6 (`alert.sos.<company>`, life-cycle open→ack→resolved, eskalasi otomatis, TTA) | B3 | ✅ |
| **FUEL SENSOR** | ⚪ belum ada → ✅ spec | FR-7.1–7.8 (parse GT06 0x0D + Teltonika AVL IO → `fuel_logs` → live-state → FUEL_DROP/REFUEL → API) | **B5a** | ⬜ not yet started |
| **DASHCAM MEDIA** | ⚪ belum ada → ✅ spec | FR-8.1–8.8 (HMAC ingest → MinIO/S3 → `media_events` ber-RBAC → presigned URL → WS `MEDIA_EVENT` → retensi) | **B5b** | ⬜ not yet started |
| **MOBILE APP** | ⚪ missing | — | (fase lanjutan) | ⬜ out of scope versi ini |
| **ANALYTICS** | ⚠️ minimal → ✅ dasar | FR-6.x (SLO/monitoring B4) | B4 / F4 | 🔄 dasar tersedia, reporting UI di frontend |
| **Multi-bahasa (i18n)** | ⚠️ | Frontend: `next-intl` locale `id` + `en-US` | F1 / F4.1 | ⬜ frontend belum mulai |

### Rekomendasi fitur → eksekusi (integritas dokumen)
Spesifikasi detil geofence/overspeed/SOS/battery/notification/route serta roadmap eksplisit
**telah termuat di dokumen ini** (§4, §5, §8, §9). Worker-alert, service-websocket & api-vehicle
telah dibangun dan diverifikasi end-to-end live (2026-08-23): semua tipe alert tercipta & terpublish,
notifikasi WS sesuai preferensi, RBAC 403 tepat, SOS escalation cap TTA tercatat. Fuel & dashcam
disetujui sebagai fase **B5a / B5b** (lihat Lampiran A #13/#14) dan termasuk dalam roadmap §8.

---

## Catatan Operasional (Runbook & Keputusan Dev)

> Bagian operasional/pengembangan — melengkapi agar operasional tim dapat merujuk, tanpa menjadi
> bagian spesifikasi produk inti.

- **Insiden (CPU/Memory/RDS/service stuck/throughput tinggi, NATS/Redis down):** ikuti prosedur otomatis & manual — panduan: dokumen insiden-runbook (runbook).
- **High Availability / failover:** model **READ REPLICA** (2 server PRIMARY + STANDBY, replika MySQL GTID / PostgreSQL streaming WAL+slot / Redis). Primary = satu-satunya jalur tulis; replika mempercepat baca (laporan/audit) via *read/write split app-level* (`internal/tenant` `ReadRouter`). Failover otomatis tidak otomatis — panduan promosi di dokumen HA (Redis mempertahankan drill failover, dicek sekali). Backup harian + uji restore = proteksi data (bukan mekanisme failover).
- **Database Provider fleksibel:** `DATABASE_PROVIDER=mysql|postgres` (default proyek = postgres). MySQL tetap selectable penuh; limitasi porting SQL controller ada pada catatan dev di dokumen provider-postgres.
- **Tenant naming:** perusahaan → database `adatrack_gps_{LOWERCASE(company_code)}`; key Redis `adatrack_gps:{company}:vehicle:state:<IMEI>`; IMEI→company_code via master `vehicle_imei_map` (upsert sinkronisasi pada service api-vehicle
saat kendaraan didaftarkan/dihapus).
- **Protokol device:** acuan resmi di `docs-device` (GT06 v1.8.1/v3.1, Teltonika). Parser memvalidasi CRC-ITU dan hanya memproses IMEI terdaftar (anti-spoofing).
