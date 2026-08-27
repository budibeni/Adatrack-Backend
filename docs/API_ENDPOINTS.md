# API Endpoint Reference — adatrack GPS Platform (Backend)

> **Audien:** Tim Frontend (Next.js) & integrasi pihak ketiga.
> **Versi dokumen:** 1.0 (2026-08-25) — bersumber langsung dari kode sumber service pada branch aktif.
> **Backend terkait:** `service-websocket` (REST + WebSocket) dan `api-vehicle` (REST manajemen).

---

## Daftar Isi

1. [Ringkasan Layanan & Base URL](#1-ringkasan-layanan--base-url)
2. [Konvensi Umum](#2-konvensi-umum)
3. [Alur Autentikasi](#3-alur-autentikasi)
4. [Endpoint Service-WebSocket (Port 8080)](#4-endpoint-service-websocket-port-8080)
5. [Endpoint API-Vehicle (Port 8081)](#5-endpoint-api-vehicle-port-8081)
6. [WebSocket Real-Time](#6-websocket-real-time)
7. [Daftar Error Code](#7-daftar-error-code)
8. [Alur End-to-End Lengkap](#8-alur-end-to-end-lengkap)

---

## 1. Ringkasan Layanan & Base URL

Sistem backend memiliki **dua layanan HTTP** yang dipakai frontend. Keduanya berbagi
JWT secret yang sama sehingga **satu token login berlaku untuk keduanya** (interop).

| Layanan | Base URL (dev lokal) | Port Default | Fungsi |
|---|---|---|---|
| `service-websocket` | `http://localhost:8080` | `8080` (`WEBSOCKET_HTTP_ADDR`) | REST utama dashboard (vehicles baca, geofences, alerts, routes) **+ WebSocket real-time** |
| `api-vehicle` | `http://localhost:8081` | `8081` (`API_VEHICLE_HTTP_ADDR`) | REST manajemen (vehicle CRUD, speed config, geofence-vehicle link, route assignment) |

Cara cepat memeriksa konektivitas:

```bash
# Service-websocket
curl -s http://localhost:8080/healthz
# => ok mysql:ok,redis:ok,nats:ok

# api-vehicle
curl -s http://localhost:8081/healthz
# => ok mysql:ok,redis:ok
```

> **Catatan produksi:** bila dideploy di balik reverse-proxy / ingress, ganti `localhost`
> dengan host yang sesuai. Semua path REST diawali prefix **`/api/v1`**.

> ⚠️ **Catatan workstation dev ini:** port host `8080` sedang dipakai container
> monitoring **cAdvisor**, sehingga service-websocket tidak bisa bind di 8080.
> Jalankan service di port lain via env proses (OS env selalu menang atas `.env`):
>
> ```bash
> WEBSOCKET_HTTP_ADDR=:18080 ./service-websocket
> API_VEHICLE_HTTP_ADDR=:18081 ./api-vehicle
> ```
>
> Sesuaikan base URL contoh curl di dokumen ini dengan port yang dipakai.
> Cek pemakai port: `docker ps --format '{{.Names}}\t{{.Ports}}'`.

**Akun demo (terverifikasi live pada environment dev saat ini):**

| Email | Password | Company | Role |
|---|---|---|---|
| `platform@adatrackgps.local` | `Platform@123` | *(konteks platform `default`)* | `SuperAdmin` |
| `admin@dev001.io` | `Admin@123` | `DEV001` | `Admin` |
| `operator@dev001.io` | `Operator@123` | `DEV001` | `Operator` |
| `driver@dev001.io` | `Driver@123` | `DEV001` | `Driver` |

> ⚠️ File seed (`backend/database/seed/master_seed.sql`) memakai domain `@def001.io`,
> tetapi database dev yang aktif berisi `@dev001.io`. Gunakan tabel di atas —
> sudah diuji langsung ke service yang berjalan.

**Vehicle demo (company `DEV001`):**

| id | IMEI | Plat Nomor | Model Device |
|---|---|---|---|
| 1 | `864201040512345` | `B 1234 XYZ` | GT06 |
| 2 | `864201040512346` | `D 5678 ABC` | Teltonika FMB920 |
| 3 | `864201040512347` | `E 9012 RST` | GT06 |

---

## 2. Konvensi Umum

### 2.1 Autentikasi — JWT Bearer

- Semua endpoint (kecuali `POST /auth/login`, `/healthz`, `/metrics`) **wajib** header:

```
Authorization: Bearer <TOKEN>
```

- JWT: **HS256**, masa berlaku **24 jam** (`JWT_EXPIRY`), berisi klaim
  `user_id`, `company_code`, `email`, `role`, `vehicle_ids`, `jti`.
- Akses token habis → minta token baru via `POST /auth/refresh` dengan `refresh_token`
  (masa berlaku default 168 jam = 7 hari, di-rotate setiap refresh).
- WebSocket menerima token lewat **query param `?token=`** atau header `Authorization`.

Cara singkat menyimpan token untuk contoh di bawah:

```bash
export TOKEN="<isi token dari response login>"
# atau otomatis (butuh jq):
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@dev001.io","password":"Admin@123"}' | jq -r '.data.token')
```

> Contoh curl di dokumen ini memakai `$TOKEN`. Bila tidak ingin install `jq`,
> salin `data.token` dari respons secara manual.

### 2.2 Envelope Respons Sukses

Semua endpoint mengembalikan format seragam:

```jsonc
{
  "status": "success",
  "data": { /* objek atau array */ },
  "pagination": { "page": 1, "limit": 100, "total": 42 }   // hanya endpoint list
}
```

Khusus **history vehicle** (`GET /vehicles/:id/history`) memakai `total_records`
(bukan `pagination.total`) karena data berformat array titik.

### 2.3 Envelope Error (GAP #3)

```jsonc
{
  "status": "error",
  "error_code": "VEHICLE_NOT_FOUND",   // kode mesin — dipakai frontend untuk i18n
  "message": "vehicle not found",      // pesan human-readable (EN)
  "timestamp": "2026-08-25T09:30:00Z"
}
```

Kode HTTP yang dipakai: `200`, `201`, `400`, `401`, `403`, `404`, `409`,
`422`, `429`, `500`, `503`. Pemetaan lengkap di [§7](#7-daftar-error-code).

### 2.4 Pagination

- Query param: `?page=` (default `1`) dan `?limit=` (default `100`, **maks `500`**).
- Respons list menyertakan `pagination: { page, limit, total }`.

Contoh:

```bash
curl -s "http://localhost:8080/api/v1/vehicles?page=2&limit=10" -H "Authorization: Bearer $TOKEN"
```

### 2.5 Rate Limit

| Cakupan | Batas | Respons |
|---|---|---|
| Login gagal | 5 percobaan / 15 menit per IP | `429 RATE_LIMIT_EXCEEDED` |
| API terautentikasi | 100 request / menit per user | `429 RATE_LIMIT_EXCEEDED` |
| WebSocket | Maks 5000 koneksi serentak | `ERROR` event `SERVER_FULL` |

### 2.6 RBAC (Hak Akses)

Role efektif = `role_override` di `user_company_access` **atau** role global di
`master.users`: `Admin`, `Manager`, `Operator`, `Driver` (+ `SuperAdmin` khusus platform).

Ringkasan perilaku default:

- **Admin** → melihat & mengelola semua vehicle/geofence/alert/routes **perusahaan-nya**.
- **Manager** → seperti Admin pada route & geofence-vehicle link (management).
- **Operator / Driver** → **hanya** data vehicle yang terdaftar di `user_vehicles`
  (row-level security). Vehicle di luar scope → `403 FORBIDDEN`.
- **SuperAdmin (konteks platform)** → HANYA dapat mengakses
  `POST /api/v1/companies` dan `POST /api/v1/users`.

> Kode contoh di dokumen ini memakai akun `admin@dev001.io` kecuali dinyatakan lain.

### 2.7 CORS & Headers

- Origin browser diizinkan bila cocok `CORS_ALLOWED_ORIGINS` (default dev: `http://localhost:3000`).
- Request `OPTIONS` preflight otomatis dijawab `204 No Content`.
- Setiap respons mendapat security headers (CSP, X-Frame-Options, HSTS, X-Content-Type-Options, dll).

### 2.8 Catatan Provider Database (mysql | postgres)

Provider dipilih via `DATABASE_PROVIDER` di `backend/.env` (PRD §7.1.1). Kontrak
REST **identik** di kedua provider, KECUALI satu limitasi yang perlu diketahui frontend:

> ⚠️ **Limitasi aktif (postgres):** respons endpoint *create* saat ini mengembalikan
> `"id": 0` karena backend memakai `LastInsertId()` yang tidak didukung driver
> PostgreSQL (pgx). INSERT-nya sendiri **berhasil** — baris tersimpan dengan ID
> sebenarnya di database.
>
> **Pola aman untuk frontend:** setelah create, anggap respons hanya indikator
> sukses/gagal (`status` + HTTP 201), lalu ambil objek lengkap beserta ID aslinya
> lewat endpoint list/detail terkait (mis. setelah `POST /routes` → `GET /api/v1/routes`
> dan cari item berdasarkan `name`/urutan terbaru).
>
> Endpoint yang terdampak: `POST /vehicles`, `POST /vehicles/:id/users`,
> `POST /speed-configs`, `POST /geofences`, `POST /routes`,
> `POST /routes/:id/assignments`. Perbaikan backend (pola `RETURNING id`)
> dicatat sebagai tindak lanjut; dokumen ini akan diperbarui saat fix dirilis.

---

## 3. Alur Autentikasi

### 3.1 Login — `POST /api/v1/auth/login`

Tersedia di **kedua service** (port 8080 & 8081). Cukup panggil salah satu untuk mendapat token.

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/auth/login` | Publik (rate limit 5/15mnt per IP) |

**Request body:**

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `email` | string | ✅ | Email terdaftar di `master.users` |
| `password` | string | ✅ | Password plaintext |

**Contoh curl:**

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@dev001.io","password":"Admin@123"}'
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",          // access JWT (24 jam)
    "token_type": "Bearer",
    "expires_in": 86400,                                          // detik
    "refresh_token": "68e2b1c0a3f9...",                           // opaque 256-bit (opsional)
    "refresh_expires_in": 604800,                                 // detik (168 jam)
    "user": {
      "id": 2,
      "company_code": "DEV001",
      "email": "admin@dev001.io",
      "role": "Admin"
    }
  }
}
```

**Error umum:** `400 INVALID_REQUEST`, `401 INVALID_CREDENTIALS`,
`403 FORBIDDEN` (company DB tidak ada / akses nonaktif), `429 RATE_LIMIT_EXCEEDED`.

### 3.2 Refresh Token — `POST /api/v1/auth/refresh`

Menukar `refresh_token` lama dengan **pasangan baru** (rotasi: token lama otomatis invalid).

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/auth/refresh` | Publik — cukup `refresh_token` valid |

**Request body:** `{ "refresh_token": "<refresh token dari login>" }`

**Contoh curl:**

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/refresh \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"68e2b1c0a3f9..."}'
```

**Respons:** sama dengan login (`token`, `token_type`, `expires_in`,
`refresh_token` baru, `refresh_expires_in`, `user`).

**Error umum:** `400 INVALID_REQUEST`, `401 UNAUTHORIZED` (refresh token invalid/expired).

### 3.3 Logout — `POST /api/v1/auth/logout`

Mencabut `jti` access token (denylist) **dan/atau** menghapus refresh token.
Rute ini **tidak** mewajibkan access token valid (tetap bisa logout walau token expired).

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/auth/logout` | Header bearer **atau** body refresh_token (minimal satu) |

**Contoh curl:**

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/logout \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"refresh_token":"68e2b1c0a3f9..."}'
```

**Respons `200 OK`:** `{ "status": "success", "data": { "message": "logged out" } }`

---

## 4. Endpoint Service-WebSocket (Port 8080)

Layanan ini adalah **sumber utama data untuk dashboard**: vehicle live list,
history playback, geofence, alert, dan route. WebSocket dibahas di [§6](#6-websocket-real-time).

### 4.0 Healthz & Metrics

| Method | Path | Keterangan |
|---|---|---|
| GET | `/healthz` | Readiness (MySQL + Redis + NATS). `200 ok ...` atau `503 degraded ...` |
| GET | `/metrics` | Metrik Prometheus (`http_*`, `ws_*`, `rbac_*`, `tenant_*`) |

```bash
curl -s http://localhost:8080/healthz
# ok mysql:ok,redis:ok,nats:ok
```

### 4.1 Platform — `POST /api/v1/companies`

Mendaftarkan **tenant baru** (auto-provision database `adatrack_gps_<CODE>` +
seluruh migrasi schema). **HANYA SuperAdmin** (login `platform@adatrackgps.local`).

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/companies` | `SuperAdmin` (konteks platform `default`) |

**Request body:**

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `code` | string | ✅ | Kode company, uppercase (mis. `ABLE01`) |
| `name` | string | ✅ | Nama perusahaan |
| `country_code` | string | ❌ | ISO 3166-1 alpha-2 (default `ID`) |
| `timezone` | string | ❌ | IANA timezone (default `Asia/Jakarta`) |
| `legal_name` | string | ❌ | Nama legal entity |
| `company_email` | string | ❌ | Email kontak resmi |
| `website` | string | ❌ | URL website |
| `tax_id` | string | ❌ | NPWP / VAT number |
| `postal_code` | string | ❌ | Kode pos |

**Contoh curl (login SuperAdmin dulu):**

```bash
PT=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"platform@adatrackgps.local","password":"Platform@123"}' | jq -r '.data.token')

curl -s -X POST http://localhost:8080/api/v1/companies \
  -H "Authorization: Bearer $PT" \
  -H 'Content-Type: application/json' \
  -d '{
        "code": "ABLE01",
        "name": "PT Abadi Logistik",
        "country_code": "ID",
        "timezone": "Asia/Jakarta",
        "legal_name": "PT Abadi Logistik Nusantara",
        "company_email": "ops@abadi.co.id",
        "website": "https://abadi.co.id",
        "tax_id": "01.234.567.8-901.000"
      }'
```

**Respons `201 Created`:**

```jsonc
{
  "status": "success",
  "data": {
    "code": "ABLE01",
    "name": "PT Abadi Logistik",
    "country_code": "ID",
    "timezone": "Asia/Jakarta",
    "database_name": "adatrack_gps_able01",
    "migrations_applied": 12
  }
}
```

**Error umum:** `403 PLATFORM_ONLY` (bukan SuperAdmin), `400 INVALID_REQUEST`,
`500 PROVISION_FAILED`.

### 4.2 Platform — `POST /api/v1/users`

Membuat **akun pertama** user tenant (atau user tambahan). **HANYA SuperAdmin**.

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/users` | `SuperAdmin` (konteks platform `default`) |

**Request body:**

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `company_code` | string | ✅ | Kode tenant tujuan (bukan `default`) |
| `email` | string | ✅ | Login identity, unik global |
| `password` | string | ✅ | Plaintext, minimal 8 karakter |
| `full_name` | string | ✅ | Nama lengkap |
| `role` | string | ❌ | `Admin` / `Manager` / `Operator` / `Driver` (default `Admin`) |
| `vehicle_ids` | number[] | ❌ | Vehicle yang di-assign (untuk Operator/Driver) |

**Contoh curl:**

```bash
curl -s -X POST http://localhost:8080/api/v1/users \
  -H "Authorization: Bearer $PT" \
  -H 'Content-Type: application/json' \
  -d '{
        "company_code": "ABLE01",
        "email": "admin@abadi.co.id",
        "password": "Abadi@123",
        "full_name": "Budi Operator",
        "role": "Admin"
      }'
```

**Respons `201 Created`:**

```jsonc
{
  "status": "success",
  "data": {
    "id": 21,
    "email": "admin@abadi.co.id",
    "full_name": "Budi Operator",
    "role": "Admin",
    "company_code": "ABLE01",
    "vehicles_assigned": 0
  }
}
```

**Error umum:** `403 PLATFORM_ONLY`, `403 PLATFORM_ROLE_RESERVED` (role `SuperAdmin`),
`400 INVALID_REQUEST` / `VEHICLE_NOT_FOUND` (vehicle bukan milik tenant).

---

## 4.3 Vehicle Endpoints

### 4.3.1 Daftar Vehicle — `GET /api/v1/vehicles`

Daftar vehicle sesuai scope RBAC + **posisi live** dari Redis (fallback MySQL).
Status vehicle pada respons bisa `active|inactive|maintenance` (DB) atau
**`ONLINE|IDLE|OFFLINE`** bila live state tersedia.

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/vehicles` | Semua role terautentikasi (hasil difilter RBAC) |

**Query params:**

| Param | Tipe | Default | Keterangan |
|---|---|---|---|
| `page` | int | 1 | Halaman |
| `limit` | int | 100 | Maks 500 |

**Contoh curl:**

```bash
curl -s "http://localhost:8080/api/v1/vehicles?page=1&limit=50" \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "id": 1,
      "imei": "864201040512345",
      "plate_number": "B 1234 XYZ",
      "device_model": "GT06",
      "status": "ONLINE",                       // ONLINE | IDLE | OFFLINE | active | inactive | maintenance
      "last_position": {
        "lat": -6.2088,
        "lon": 106.8456,
        "speed": 42.5,
        "timestamp": "2026-08-25T09:29:58Z"
      }
    }
  ],
  "pagination": { "page": 1, "limit": 50, "total": 3 }
}
```

> `last_position` = `null` bila belum ada telemetri sama sekali.

### 4.3.2 Detail Vehicle — `GET /api/v1/vehicles/:id`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/vehicles/:id` | Semua role terautentikasi **dengan akses vehicle tsb** |

**Contoh curl:**

```bash
curl -s http://localhost:8080/api/v1/vehicles/1 -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** objek sama dengan item list di atas (tanpa pagination).

**Error umum:** `400 INVALID_PARAM`, `404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

### 4.3.3 History Playback — `GET /api/v1/vehicles/:id/history`

Titik telemetri historis untuk playback peta (SLA: window 30 hari < 1.5 detik).
Data dikembalikan **kronologis ASC** siap diputar.

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/vehicles/:id/history` | Semua role terautentikasi dengan akses vehicle |

**Query params:**

| Param | Tipe | Default | Keterangan |
|---|---|---|---|
| `start` | RFC3339 | now − 24 jam | Awal window (mis. `2026-08-01T00:00:00Z`) |
| `end` | RFC3339 | now | Akhir window — **maks 30 hari** dari start |
| `limit` | int | 5000 | Maks 10000 titik |

**Contoh curl:**

```bash
curl -s "http://localhost:8080/api/v1/vehicles/1/history?start=2026-08-24T00:00:00Z&end=2026-08-24T23:59:59Z&limit=2000" \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "lat": -6.2088,
      "lon": 106.8456,
      "speed": 42.5,
      "heading": 135.0,
      "timestamp": "2026-08-24T00:00:05Z"
    }
  ],
  "total_records": 17280
}
```

> **Catatan:** `total_records` mengikuti aturan `omitempty` JSON — field ini
> **tidak muncul** (bukan `0`) bila hasil kosong. Perlakukan ketidakhadiran
> field sebagai `0` titik.

**Error umum:** `400 INVALID_PARAM`
(`start/end bukan RFC3339`, `end <= start`, `window > 30 hari`, `limit di luar 1..10000`),
`404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

---

## 4.4 Geofence Endpoints

Skema geofence: `area_type` = `circle` (pakai `coordinates` GeoJSON Point + `radius_meters`)
atau `polygon` (pakai `coordinates` GeoJSON Polygon; `boundary_points` opsional berupa
array `{lat, lon}`). Deteksi breach dijalankan worker-alert real-time.

### 4.4.1 Daftar Geofence — `GET /api/v1/geofences`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/geofences` | Semua role terautentikasi. Non-admin hanya geofence yang punya vehicle dalam scope-nya |

**Contoh curl:**

```bash
curl -s http://localhost:8080/api/v1/geofences -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "id": 1,
      "name": "Depot Jakarta",
      "area_type": "circle",
      "coordinates": { "type": "Point", "coordinates": [106.844, -6.2088] },
      "radius_meters": 5000,
      "created_by": 2,
      "is_active": true,
      "vehicles": [1, 2, 3],
      "created_at": "2026-08-23T08:00:00Z"
    }
  ]
}
```

> Untuk polygon: `coordinates` berisi GeoJSON Polygon dan `boundary_points`
> berupa `[{"lat": -6.2, "lon": 106.8}, ...]`.

### 4.4.2 Detail Geofence — `GET /api/v1/geofences/:id`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/geofences/:id` | Semua role dengan akses (lihat §2.6) |

```bash
curl -s http://localhost:8080/api/v1/geofences/1 -H "Authorization: Bearer $TOKEN"
```

**Error umum:** `400 INVALID_PARAM`, `404 GEOFENCE_NOT_FOUND`, `403 FORBIDDEN`.

### 4.4.3 Buat Geofence — `POST /api/v1/geofences` (Admin)

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/geofences` | **Admin** saja |

**Request body:**

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `name` | string | ✅ | Nama zona |
| `area_type` | string | ✅ | `circle` atau `polygon` |
| `coordinates` | object | ✅ | **GeoJSON valid** |
| `radius_meters` | number | circle ✅ | Wajib > 0 bila `area_type=circle` |
| `boundary_points` | array | ❌ | `[{lat, lon}, ...]` untuk polygon |
| `vehicle_ids` | number[] | ❌ | Vehicle yang ditautkan ke zona ini |

**Contoh curl — circle:**

```bash
curl -s -X POST http://localhost:8080/api/v1/geofences \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "Gudang Bandung",
        "area_type": "circle",
        "coordinates": { "type": "Point", "coordinates": [107.6098, -6.9147] },
        "radius_meters": 2000,
        "vehicle_ids": [1]
      }'
```

**Contoh curl — polygon:**

```bash
curl -s -X POST http://localhost:8080/api/v1/geofences \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "Zona Pelabuhan",
        "area_type": "polygon",
        "coordinates": {
          "type": "Polygon",
          "coordinates": [[
            [106.84, -6.20], [106.90, -6.20], [106.90, -6.26],
            [106.84, -6.26], [106.84, -6.20]
          ]]
        },
        "boundary_points": [
          { "lat": -6.20, "lon": 106.84 },
          { "lat": -6.20, "lon": 106.90 },
          { "lat": -6.26, "lon": 106.90 },
          { "lat": -6.26, "lon": 106.84 }
        ]
      }'
```

**Respons `201 Created`:** `{ "status": "success", "data": { "id": 5 } }`

> ⚠️ Provider postgres: `id` bisa `0` — lihat §2.8 (Catatan Provider Database).

**Error umum:** `403 FORBIDDEN` ("only admins can manage geofences"),
`400 INVALID_PARAM` (`area_type salah`, `coordinates bukan GeoJSON`,
`radius_meters <= 0`), `403 FORBIDDEN` bila menautkan vehicle di luar scope.

### 4.4.4 Hapus Geofence — `DELETE /api/v1/geofences/:id` (Admin)

Soft delete (`is_active=false`) — geofence tidak lagi memicu alert.

| Method | Path | Auth |
|---|---|---|
| DELETE | `/api/v1/geofences/:id` | **Admin** saja |

```bash
curl -s -X DELETE http://localhost:8080/api/v1/geofences/5 -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 5, "deleted": true } }`

---

## 4.5 Alert Endpoints

Alert dibuat otomatis oleh `worker-alert` dari telemetri real-time. Frontend
membaca alert di sini dan menerima push notifikasi via WebSocket (`ALERT_NOTIFICATION`, §6).

**Nilai enum penting:**

| Kolom | Nilai |
|---|---|
| `alert_type` | `GEOFENCE_BREACH` · `OVERSPEEDING` · `OFFLINE` · `BATTERY_LOW` · `SOS` · `ROUTE_DEVIATION` |
| `severity` | `low` · `medium` · `high` · `critical` |
| `status` | `open` → `acknowledged` → `resolved` |

### 4.5.1 Daftar Alert — `GET /api/v1/alerts`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/alerts` | Semua role terautentikasi (difilter RBAC per vehicle) |

**Query params:**

| Param | Tipe | Default | Keterangan |
|---|---|---|---|
| `status` | string | *(semua)* | Filter `open` / `acknowledged` / `resolved` |
| `page` | int | 1 | Halaman |
| `limit` | int | 100 | Maks 500 |

**Contoh curl:**

```bash
# Semua alert terbuka, halaman 1, 50 baris
curl -s "http://localhost:8080/api/v1/alerts?status=open&page=1&limit=50" \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "id": 12,
      "vehicle_id": 1,
      "imei": "864201040512345",
      "alert_type": "OVERSPEEDING",
      "severity": "critical",
      "description": "Speed 150 km/h exceeds limit 80 km/h",
      "status": "open",
      "vehicle_lat": -6.2088,
      "vehicle_lon": 106.8456,
      "created_at": "2026-08-25T09:30:00Z"
    }
  ],
  "pagination": { "page": 1, "limit": 50, "total": 1 }
}
```

> Field opsional yang bisa muncul: `acknowledged_by`, `acknowledged_at`,
> `resolved_at`. Untuk SOS gunakan kombinasi `?status=open` + filter klien
> pada `alert_type === "SOS"`.

### 4.5.2 Acknowledge Alert — `PATCH /api/v1/alerts/:id/acknowledge`

Menandai alert sebagai ditangani. **Wajib untuk life-cycle SOS** — menghentikan
eskalasi otomatis dan mencatat Time-To-Acknowledge (TTA).

| Method | Path | Auth |
|---|---|---|
| PATCH | `/api/v1/alerts/:id/acknowledge` | Semua role dengan akses vehicle pemilik alert |

Tidak butuh request body.

**Contoh curl:**

```bash
curl -s -X PATCH http://localhost:8080/api/v1/alerts/12/acknowledge \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** objek alert ter-update:

```jsonc
{
  "status": "success",
  "data": {
    "id": 12,
    "vehicle_id": 1,
    "imei": "864201040512345",
    "alert_type": "OVERSPEEDING",
    "severity": "critical",
    "status": "acknowledged",
    "acknowledged_by": 2,
    "acknowledged_at": "2026-08-25T09:31:22Z",
    "created_at": "2026-08-25T09:30:00Z"
  }
}
```

**Error umum:** `400 INVALID_PARAM`, `404 ALERT_NOT_FOUND`,
`400 ALERT_NOT_UPDATABLE` (alert sudah `resolved`), `403 FORBIDDEN`.

---

## 4.6 Route Endpoints (Dashboard)

Route = rencana perjalanan berisi waypoint yang di-assign ke vehicle+driver.
Worker-alert mendeteksi **ROUTE_DEVIATION** real-time; endpoint ini untuk
manajemen & pemantauan dari dashboard. Endpoint tulis di service ini **Admin-only**
(versi Admin/Manager dengan assignment penuh ada di api-vehicle, §5.5).

### 4.6.1 Daftar Route — `GET /api/v1/routes`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/routes` | Semua role terautentikasi. Non-admin hanya route dengan assignment ke vehicle dalam scope-nya atau dirinya sebagai driver |

```bash
curl -s http://localhost:8080/api/v1/routes -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "id": 3,
      "name": "Depot - Pelabuhan Tanjung Priok",
      "waypoints": [ { "lat": -6.2088, "lon": 106.8456 }, { "lat": -6.1100, "lon": 106.9300 } ],
      "estimated_duration_sec": 5400,
      "created_by": 2,
      "is_active": true,
      "assignments": [
        {
          "id": 7,
          "route_id": 3,
          "vehicle_id": 1,
          "driver_user_id": 4,
          "status": "in_progress",
          "started_at": "2026-08-25T02:00:00Z",
          "deviation_meters": 35.2,
          "imei": "864201040512345"
        }
      ],
      "created_at": "2026-08-24T10:00:00Z",
      "updated_at": "2026-08-24T10:00:00Z"
    }
  ]
}
```

### 4.6.2 Detail Route — `GET /api/v1/routes/:id`

Struktur respons sama dengan item list di atas.

```bash
curl -s http://localhost:8080/api/v1/routes/3 -H "Authorization: Bearer $TOKEN"
```

**Error umum:** `400 INVALID_PARAM`, `404 ROUTE_NOT_FOUND`, `403 FORBIDDEN`.

### 4.6.3 Buat Route — `POST /api/v1/routes` (Admin)

| Method | Path | Auth |
|---|---|---|
| POST | `/api/v1/routes` | **Admin** saja |

**Request body:**

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `name` | string | ✅ | Nama rute |
| `waypoints` | array | ✅ | Minimal 2 titik `{lat, lon}` |
| `estimated_duration_sec` | int | ❌ | Estimasi durasi tempuh |
| `vehicle_ids` | number[] | ❌ | Langsung buat assignment per vehicle |
| `driver_user_id` | uint64 | ❌ | Driver pada assignment yang dibuat |

```bash
curl -s -X POST http://localhost:8080/api/v1/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "Depot - Gudang Bandung",
        "waypoints": [
          { "lat": -6.2088, "lon": 106.8456 },
          { "lat": -6.5500, "lon": 107.1500 },
          { "lat": -6.9147, "lon": 107.6098 }
        ],
        "estimated_duration_sec": 14400
      }'
```

**Respons `201 Created`:** objek route lengkap (seperti detail).

### 4.6.4 Update Route — `PATCH /api/v1/routes/:id` (Admin)

Partial update. Field dikirim apa adanya — field yang tidak dikirim tidak berubah.
`vehicle_ids` di sini **menambahkan** assignment baru (append).

```bash
curl -s -X PATCH http://localhost:8080/api/v1/routes/3 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "name": "Depot - Pelabuhan (revisi)", "estimated_duration_sec": 6000 }'
```

**Error umum:** `403 FORBIDDEN`, `400 INVALID_REQUEST`
(`waypoints < 2`, `name kosong`, body kosong), `404 ROUTE_NOT_FOUND`.

### 4.6.5 Hapus Route — `DELETE /api/v1/routes/:id` (Admin)

Soft delete (`is_active=false`); worker-alert berhenti memantau route ini.

```bash
curl -s -X DELETE http://localhost:8080/api/v1/routes/3 -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 3, "deleted": true } }`

### 4.6.6 Live Track Route — `GET /api/v1/routes/:id/track`

Status assignment live + deviasi terhadap waypoint (dipakai UI pemantauan rute).

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/routes/:id/track` | Semua role dengan akses route |

```bash
curl -s http://localhost:8080/api/v1/routes/3/track -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": {
    "route_id": 3,
    "name": "Depot - Pelabuhan Tanjung Priok",
    "assignments": [
      {
        "id": 7,
        "route_id": 3,
        "vehicle_id": 1,
        "driver_user_id": 4,
        "status": "delayed",
        "started_at": "2026-08-25T02:00:00Z",
        "deviation_meters": 512.7,
        "imei": "864201040512345"
      }
    ]
  }
}
```

> Nilai `status` assignment: `not_started` · `in_progress` · `completed` · `delayed`.
> `deviation_meters` = deviasi maksimum tercatat vs waypoint terdekat.

---

## 5. Endpoint API-Vehicle (Port 8081)

Layanan manajemen (tulis): CRUD vehicle, assignment user↔vehicle, speed config,
tautan geofence↔vehicle, serta route+assignment versi Admin/Manager.
Semua path di bawah prefix `/api/v1` pada **`http://localhost:8081`**.

| Method | Path | Keterangan |
|---|---|---|
| GET | `/healthz` | Readiness (MySQL + Redis) |
| GET | `/metrics` | Metrik Prometheus |

```bash
curl -s http://localhost:8081/healthz   # ok mysql:ok,redis:ok
```

### 5.1 Vehicle CRUD

#### 5.1.1 Daftar Vehicle — `GET /api/v1/vehicles`

Mirip §4.3.1 tetapi bentuk item lebih detail (field enterprise migration 002)
dan **tanpa** overlay posisi live.

| Query param | Nilai | Keterangan |
|---|---|---|
| `status` | `active` / `inactive` / `maintenance` | Filter status DB |
| `page`, `limit` | int | Pagination (maks 500) |

```bash
curl -s "http://localhost:8081/api/v1/vehicles?status=active&page=1&limit=50" \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    {
      "id": 1,
      "imei": "864201040512345",
      "plate_number": "B 1234 XYZ",
      "make": "Toyota",
      "model": "Hilux",
      "fuel_type": "diesel",
      "vehicle_type_code": "PICKUP_TRUCK",
      "driver_user_id": null,
      "device_model": "GT06",
      "status": "active",
      "created_at": "2026-08-20T10:00:00Z"
    }
  ],
  "pagination": { "page": 1, "limit": 50, "total": 3 }
}
```

#### 5.1.2 Detail Vehicle — `GET /api/v1/vehicles/:id`

```bash
curl -s http://localhost:8081/api/v1/vehicles/1 -H "Authorization: Bearer $TOKEN"
```

Respons: objek sama dengan item list. Error: `404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.1.3 Buat Vehicle — `POST /api/v1/vehicles` (Admin)

Mendaftarkan kendaraan + perangkat GPS. Otomatis menyinkronkan `master.vehicle_imei_map`
agar **ingestion-tcp langsung menerima data dari IMEI baru** (anti-spoofing allowlist).

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `imei` | string | ✅ | IMEI device (unik per company) |
| `plate_number` | string | ✅ | Plat nomor |
| `make`, `model`, `variant` | string | ❌ | Merek / model / varian |
| `year_of_manufacture` | int | ❌ | Tahun pembuatan |
| `color` | string | ❌ | Warna |
| `fuel_type` | string | ❌ | `petrol`/`diesel`/`electric`/`hybrid`/`CNG`/`LPG`/`hydrogen` |
| `vehicle_category_code` | string | ❌ | Kategori (`LCV`, `HCV`, `PVB`, ...) |
| `vehicle_type_code` | string | ❌ | Tipe (`SEDAN`, `PICKUP_TRUCK`, ...) |
| `driver_user_id` | uint64 | ❌ | Driver utama |
| `device_model` | string | ❌ | Mis. `GT06`, `Teltonika FMB920` |
| `firmware_version` | string | ❌ | Firmware device |
| `registration_number` | string | ❌ | No. registrasi |
| `notes` | string | ❌ | Catatan bebas |

```bash
curl -s -X POST http://localhost:8081/api/v1/vehicles \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "imei": "864201040512348",
        "plate_number": "B 9999 DEF",
        "make": "Mitsubishi",
        "model": "L300",
        "year_of_manufacture": 2020,
        "color": "White",
        "fuel_type": "diesel",
        "vehicle_category_code": "LCV",
        "vehicle_type_code": "VAN",
        "device_model": "GT06",
        "firmware_version": "V3.4"
      }'
```

**Respons `201 Created`:**

```jsonc
{ "status": "success", "data": { "id": 4, "imei": "864201040512348" } }
```

> ⚠️ Provider postgres: `id` bisa `0` — lihat §2.8 (Catatan Provider Database).

**Error umum:** `400 INVALID_REQUEST`, `409 DUPLICATE` (IMEI sudah terdaftar), `403 FORBIDDEN`.

---

#### 5.1.4 Update Vehicle — `PATCH /api/v1/vehicles/:id` (Admin)

Partial update; hanya field yang dikirim yang berubah.

| Field | Tipe | Keterangan |
|---|---|---|
| `plate_number`, `make`, `model`, `color` | string | Data umum |
| `fuel_type` | string | Enum bahan bakar |
| `device_model`, `firmware_version` | string | Perangkat |
| `registration_number`, `notes` | string | Administratif |
| `status` | string | `active` / `inactive` / `maintenance` |
| `driver_user_id` | uint64 | Driver utama |

```bash
curl -s -X PATCH http://localhost:8081/api/v1/vehicles/4 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "status": "maintenance", "notes": "Servis rutin 40.000 km" }'
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 4 } }`

**Error umum:** `400 INVALID_STATUS` (status di luar enum), `400 EMPTY_UPDATE`,
`404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.1.5 Hapus Vehicle — `DELETE /api/v1/vehicles/:id` (Admin)

Soft delete (`deleted_at=NOW()`, status → `inactive`) **dan** menghapus mapping
`vehicle_imei_map` sehingga data dari IMEI tersebut **ditolak ingestion**
(perilaku anti-spoofing).

```bash
curl -s -X DELETE http://localhost:8081/api/v1/vehicles/4 -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 4 } }`

---

### 5.2 Assignment User ↔ Vehicle

Mengelola siapa saja (Operator/Driver) yang boleh melihat sebuah vehicle
(`user_vehicles`) — sumber row-level security untuk semua endpoint.

#### 5.2.1 Daftar User pada Vehicle — `GET /api/v1/vehicles/:id/users`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/vehicles/:id/users` | Semua role dengan akses vehicle tsb |

```bash
curl -s http://localhost:8081/api/v1/vehicles/1/users -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    { "user_id": 3, "role": "Operator", "assigned_at": "2026-08-23T09:00:00Z" },
    { "user_id": 4, "role": "Driver",   "assigned_at": "2026-08-23T09:00:00Z" }
  ]
}
```

#### 5.2.2 Assign User — `POST /api/v1/vehicles/:id/users` (Admin)

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `user_id` | uint64 | ✅ | ID master user (`data.user.id` dari login user tsb) |

```bash
curl -s -X POST http://localhost:8081/api/v1/vehicles/3/users \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"user_id": 4}'
```

**Respons `201 Created`:**

```jsonc
{ "status": "success", "data": { "vehicle_id": 3, "user_id": 4 } }
```

**Error umum:** `400 INVALID_REQUEST`, `404 VEHICLE_NOT_FOUND`,
`422 USER_NOT_IN_COMPANY` (user tidak punya akses ke company ini), `403 FORBIDDEN`.

#### 5.2.3 Unassign User — `DELETE /api/v1/vehicles/:id/users/:userId` (Admin)

```bash
curl -s -X DELETE http://localhost:8081/api/v1/vehicles/3/users/4 \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "vehicle_id": 3, "user_id": 4 } }`

---

### 5.3 Speed Configs

Konfigurasi batas kecepatan yang dibaca `worker-alert` untuk deteksi **OVERSPEEDING**.
Dua level: **global** (`vehicle_id` tidak dikirim / `null`) dan **per-vehicle**
(per-vehicle menimpa global).

#### 5.3.1 Daftar — `GET /api/v1/speed-configs`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/speed-configs` | Semua role terautentikasi |

```bash
curl -s http://localhost:8081/api/v1/speed-configs -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{
  "status": "success",
  "data": [
    { "id": 1, "vehicle_id": null,  "speed_limit_kmh": 80, "grace_margin_kmh": 10, "is_active": true,  "created_at": "..." },
    { "id": 2, "vehicle_id": 1,     "speed_limit_kmh": 60, "grace_margin_kmh": 5,  "is_active": true,  "created_at": "..." }
  ]
}
```

> Urutan respons: konfigurasi global (`vehicle_id: null`) selalu paling atas.

#### 5.3.2 Buat — `POST /api/v1/speed-configs` (Admin)

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `speed_limit_kmh` | number | ✅ | Batas kecepatan, > 0 |
| `grace_margin_kmh` | number | ❌ | Toleransi (default 0) |
| `vehicle_id` | uint64 | ❌ | Hilangkan untuk konfigurasi **global** |

```bash
# Per-vehicle
curl -s -X POST http://localhost:8081/api/v1/speed-configs \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "vehicle_id": 2, "speed_limit_kmh": 70, "grace_margin_kmh": 5 }'

# Global default
curl -s -X POST http://localhost:8081/api/v1/speed-configs \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "speed_limit_kmh": 80, "grace_margin_kmh": 10 }'
```

**Respons `201 Created`:** `{ "status": "success", "data": { "id": 3 } }`

> ⚠️ Provider postgres: `id` bisa `0` — lihat §2.8 (Catatan Provider Database).

**Error umum:** `400 INVALID_REQUEST` (`speed_limit_kmh <= 0`),
`404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.3.3 Update — `PATCH /api/v1/speed-configs/:id` (Admin)

```bash
curl -s -X PATCH http://localhost:8081/api/v1/speed-configs/2 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "is_active": false }'
```

**Error umum:** `400 EMPTY_UPDATE`, `404 SPEED_CONFIG_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.3.4 Hapus — `DELETE /api/v1/speed-configs/:id` (Admin)

```bash
curl -s -X DELETE http://localhost:8081/api/v1/speed-configs/2 \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 2 } }`

---

### 5.3b Fuel Sensor (B5a — PRD v1.3.0 Module 7)

Kanal bahan bakar end-to-end: parser GT06 `0x0D` (!AIOIL) + Teltonika AVL IO →
`fuel_logs` (migration 013, partisi bulanan) → live state `fuel_level` → alert
**FUEL_DROP** (critical) / **REFUEL** (medium) via threshold `fuel_configs`
(migration 014; baris per-vehicle menang atas global `vehicle_id IS NULL`) →
subject NATS `alert.fuel.<company>` + notifikasi tipe `fuel_drop`/`refuel`.

#### 5.3b.1 Riwayat BBM — `GET /api/v1/vehicles/:id/fuel/history`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/vehicles/:id/fuel/history` | Semua role terautentikasi dengan akses vehicle (`user_vehicles`; admin company = semua) |

**Query params:**

| Param | Wajib | Default | Keterangan |
|---|---|---|---|
| `from` | tidak | now-24h | RFC3339, mis. `2026-08-01T00:00:00Z` |
| `to` | tidak | now | RFC3339; window maksimum **30 hari** |
| `limit` | tidak | 5000 | 1–10000 titik |

```bash
curl -s "http://localhost:8081/api/v1/vehicles/1/fuel/history?from=2026-08-20T00:00:00Z&to=2026-08-21T00:00:00Z" \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`** (format GAP #1/GAP #3 — data array titik + sibling `total_records`):

```json
{
  "status": "success",
  "data": [
    { "timestamp": "2026-08-20T10:05:00Z", "fuel_level": 27.14, "fuel_temp_c": 25.4 },
    { "timestamp": "2026-08-20T09:55:00Z", "fuel_level": 38.2, "fuel_volume": 120.5 }
  ],
  "total_records": 2
}
```

**Error umum:** `400 INVALID_PARAM`, `404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.3b.2 Daftar Konfigurasi — `GET /api/v1/fuel-configs`

```bash
curl -s http://localhost:8081/api/v1/fuel-configs -H "Authorization: Bearer $TOKEN"
```

**Respons:** array `{ id, vehicle_id (null=global), drop_threshold,
refuel_threshold, window_seconds, enabled, created_at }`.

#### 5.3b.3 Buat — `POST /api/v1/fuel-configs` (Admin/adatrack Manager)

```bash
# Global default company
curl -s -X POST http://localhost:8081/api/v1/fuel-configs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{ "drop_threshold": 15, "refuel_threshold": 20, "window_seconds": 300 }'

# Per-vehicle (menimpa global)
curl -s -X POST http://localhost:8081/api/v1/fuel-configs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{ "vehicle_id": 1, "drop_threshold": 25 }'
```

**Error umum:** `400 INVALID_REQUEST`, `404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.
Respons `201 Created`: `{ "status": "success", "data": { "id": 3 } }`.

#### 5.3b.4 Update — `PATCH /api/v1/fuel-configs/:id` (Admin/adatrack Manager)

```bash
curl -s -X PATCH http://localhost:8081/api/v1/fuel-configs/1 \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{ "drop_threshold": 18, "enabled": false }'
```

**Error umum:** `400 EMPTY_UPDATE` / `INVALID_REQUEST`, `404 FUEL_CONFIG_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.3b.5 Hapus — `DELETE /api/v1/fuel-configs/:id` (Admin/adatrack Manager)

```bash
curl -s -X DELETE http://localhost:8081/api/v1/fuel-configs/1 \
  -H "Authorization: Bearer $TOKEN"
```

**Env terkait (fallback saat fuel_configs kosong):** `FUEL_DROP_THRESHOLD`,
`FUEL_REFUEL_THRESHOLD`, `FUEL_WINDOW_SECONDS`, `TELTONIKA_IO_FUEL_LEVEL/USED/TEMP`.

---

### 5.4 Geofence ↔ Vehicle Link

Mengelola vehicle mana saja yang dipantau oleh sebuah geofence
(`geofence_vehicles`). Membuat geofence sendiri tetap lewat service-websocket (§4.4.3).

#### 5.4.1 Daftar Vehicle Tertaut — `GET /api/v1/geofences/:id/vehicles`

| Method | Path | Auth |
|---|---|---|
| GET | `/api/v1/geofences/:id/vehicles` | Semua role terautentikasi; non-admin hanya melihat vehicle dalam scope-nya |

```bash
curl -s http://localhost:8081/api/v1/geofences/1/vehicles -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:**

```jsonc
{ "status": "success", "data": { "geofence_id": 1, "vehicles": [1, 2, 3] } }
```

#### 5.4.2 Tautkan Vehicle — `POST /api/v1/geofences/:id/vehicles` (Admin/Manager)

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `vehicle_id` | uint64 | ✅ | Vehicle yang akan dipantau geofence ini |

```bash
curl -s -X POST http://localhost:8081/api/v1/geofences/1/vehicles \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"vehicle_id": 2}'
```

**Respons `201 Created`:**

```jsonc
{ "status": "success", "data": { "geofence_id": 1, "vehicle_id": 2 } }
```

**Error umum:** `400 INVALID_REQUEST`, `404 GEOFENCE_NOT_FOUND`,
`404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.4.3 Lepas Tautan — `DELETE /api/v1/geofences/:id/vehicles/:vehicleId` (Admin/Manager)

```bash
curl -s -X DELETE http://localhost:8081/api/v1/geofences/1/vehicles/2 \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "geofence_id": 1, "vehicle_id": 2 } }`

**Error umum:** `404 LINK_NOT_FOUND` (tautan tidak ada), `403 FORBIDDEN`.

---

### 5.5 Routes + Assignments (Manajemen)

Versi manajemen penuh dari route (§4.6): create/update/delete route diizinkan
untuk **Admin & Manager**, plus endpoint assignment eksplisit.

#### 5.5.1 Daftar Route — `GET /api/v1/routes`

```bash
curl -s http://localhost:8081/api/v1/routes -H "Authorization: Bearer $TOKEN"
```

Respons: array route dengan `assignments` ter-embed (struktur seperti §4.6.1,
tanpa field `imei` pada assignment dan tanpa `updated_at`).

#### 5.5.2 Buat Route — `POST /api/v1/routes` (Admin/Manager)

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `name` | string | ✅ | Nama rute |
| `waypoints` | array | ✅ | Minimal 2 titik `{lat, lon}` |
| `estimated_duration_sec` | int | ❌ | Estimasi durasi |

```bash
curl -s -X POST http://localhost:8081/api/v1/routes \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
        "name": "Rute Pagi Surabaya - Malang",
        "waypoints": [
          { "lat": -7.2575, "lon": 112.7521 },
          { "lat": -7.9666, "lon": 112.6326 }
        ],
        "estimated_duration_sec": 7200
      }'
```

**Respons `201 Created`:**

```jsonc
{ "status": "success", "data": { "id": 4, "name": "Rute Pagi Surabaya - Malang" } }
```

> ⚠️ Provider postgres: `id` bisa `0` — lihat §2.8 (Catatan Provider Database).

#### 5.5.3 Detail / Update / Hapus Route

```bash
# Detail (dengan assignments)
curl -s http://localhost:8081/api/v1/routes/4 -H "Authorization: Bearer $TOKEN"

# Update partial (Admin/Manager)
curl -s -X PATCH http://localhost:8081/api/v1/routes/4 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "name": "Rute Pagi (revisi)", "is_active": true }'

# Soft delete (Admin/Manager)
curl -s -X DELETE http://localhost:8081/api/v1/routes/4 -H "Authorization: Bearer $TOKEN"
```

Error umum: `400 EMPTY_UPDATE` / `INVALID_REQUEST`, `404 ROUTE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.5.4 Assign Route ke Vehicle+Driver — `POST /api/v1/routes/:id/assignments`

Membuat baris assignment baru dengan status awal `not_started`.
Worker-alert akan mulai memantau perjalanan ini otomatis (refresh berkala ≤ 40 dtk).

| Field | Tipe | Wajib | Keterangan |
|---|---|---|---|
| `vehicle_id` | uint64 | ✅ | Vehicle pelaksana |
| `driver_user_id` | uint64 | ✅ | ID master user driver |

```bash
curl -s -X POST http://localhost:8081/api/v1/routes/3/assignments \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{ "vehicle_id": 1, "driver_user_id": 4 }'
```

**Respons `201 Created`:**

```jsonc
{ "status": "success",
  "data": { "id": 8, "route_id": 3, "vehicle_id": 1, "driver_user_id": 4 } }
```

> ⚠️ Provider postgres: `id` bisa `0` — lihat §2.8 (Catatan Provider Database);
> ambil id asli via `GET /api/v1/routes/:id/track` (field `assignments[].id`).

**Error umum:** `400 INVALID_REQUEST`, `404 ROUTE_NOT_FOUND`
(rute tidak ada / inactive), `404 VEHICLE_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.5.5 Ubah Status Assignment — `PATCH /api/v1/routes/:id/assignments/:assignmentId`

Transisi status manual oleh Admin/Manager. Nilai valid:
`not_started` · `in_progress` · `completed` · `delayed`.
Sistem mengisi `started_at` otomatis saat pertama kali `in_progress`,
dan `completed_at` saat `completed`.

```bash
curl -s -X PATCH http://localhost:8081/api/v1/routes/3/assignments/8 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"status": "in_progress"}'
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 8, "status": "in_progress" } }`

**Error umum:** `400 INVALID_REQUEST` (status di luar enum),
`404 ASSIGNMENT_NOT_FOUND`, `403 FORBIDDEN`.

#### 5.5.6 Unassign — `DELETE /api/v1/routes/:id/assignments/:assignmentId`

```bash
curl -s -X DELETE http://localhost:8081/api/v1/routes/3/assignments/8 \
  -H "Authorization: Bearer $TOKEN"
```

**Respons `200 OK`:** `{ "status": "success", "data": { "id": 8 } }`

---

## 6. WebSocket Real-Time

Koneksi real-time untuk update posisi & notifikasi alert, dibuka di
**service-websocket (port 8080)**:

```
ws://localhost:8080/api/v1/ws?token=<JWT>
```

- Token dikirim via **query param `?token=`** (paling umum) atau header `Authorization: Bearer ...`.
- Koneksi tanpa/invalid token → HTTP `401 UNAUTHORIZED` (envelope error JSON).
- RBAC diterapkan saat handshake: user hanya menerima event vehicle milik company-nya.
- **Heartbeat:** server mengirim ping setiap ±30 detik; library WebSocket modern
  membalas pong otomatis. Koneksi idle tanpa pong akan diputus oleh server.
- **Reconnect:** frontend wajib implement auto-reconnect dengan exponential backoff (FR-5.3).

### 6.1 Pesan dari Client → Server

Client hanya mengirim kontrol message berikut (JSON):

```jsonc
// Berlangganan update satu vehicle (hanya berhasil bila punya akses)
{ "event": "SUBSCRIBE",   "data": { "vehicle_id": 1 } }

// Berhenti berlangganan
{ "event": "UNSUBSCRIBE", "data": { "vehicle_id": 1 } }
```

### 6.2 Event Server → Client

#### `CONNECTION_STATUS` — dikirim segera setelah koneksi terbuka

```jsonc
{
  "event": "CONNECTION_STATUS",
  "data": { "status": "connected", "user_id": 2, "company_code": "DEV001" }
}
```

#### `SUBSCRIBED` — konfirmasi SUBSCRIBE yang berhasil

```jsonc
{ "event": "SUBSCRIBED", "data": { "vehicle_id": 1 } }
```

#### `ERROR` — kegagalan (misal subscribe vehicle tanpa hak akses)

```jsonc
{
  "event": "ERROR",
  "error_code": "UNAUTHORIZED_VEHICLE",
  "message": "You don't have access to this vehicle"
}
```

Kode error WS lain: `SERVER_FULL` (maks koneksi tercapai), `INTERNAL_ERROR`.

#### `VEHICLE_UPDATE` — posisi live (dari telemetri device, latensi < 800 ms)

```jsonc
{
  "event": "VEHICLE_UPDATE",
  "data": {
    "vehicle_id": 1,
    "imei": "864201040512345",
    "company_code": "DEV001",
    "plate_number": "B 1234 XYZ",
    "device_model": "GT06",
    "lat": -6.2088,
    "lon": 106.8456,
    "speed": 42.5,
    "heading": 135,
    "acc": true,
    "status": "MOVING",            // MOVING (speed>0) | IDLE
    "battery": 95,                  // opsional (%)
    "timestamp": "2026-08-25T09:30:05Z"
  }
}
```

> Catatan: event ini hanya sampai ke client yang **telah subscribe** (`SUBSCRIBE`)
> atau — sesuai implementasi hub — client dengan hak akses pada vehicle tersebut.

#### `ALERT_NOTIFICATION` — push alert real-time (worker-alert → WS)

```jsonc
{
  "event": "ALERT_NOTIFICATION",
  "data": {
    "alert_id": "12",
    "vehicle_id": 1,
    "imei": "864201040512345",
    "company_code": "DEV001",
    "alert_type": "SOS",             // GEOFENCE_BREACH | OVERSPEEDING | OFFLINE | BATTERY_LOW | SOS | ROUTE_DEVIATION
    "severity": "critical",
    "status": "open",                 // open | acknowledged | resolved
    "message": "SOS triggered by driver",
    "lat": -6.2088,
    "lon": 106.8456,
    "speed": 0,
    "triggered_at": 1770000000,       // unix epoch detik
    "geofence_id": 1,                 // hanya GEOFENCE_BREACH
    "geofence_name": "Depot Jakarta",
    "speed_limit": 80,                // hanya OVERSPEEDING
    "speed_observed": 150
  }
}
```

Notifikasi hanya dikirim ke user yang memenuhi `notification_preferences`
(channel websocket aktif + severity memenuhi ambang) dan berhak atas vehicle tsb.

### 6.3 Contoh Uji Cepat dengan `websocat`

```bash
# install: cargo install websocat  (atau gunakan wscat: npm i -g wscat)
websocat "ws://localhost:8080/api/v1/ws?token=$TOKEN"

# lalu ketik:
{"event":"SUBSCRIBE","data":{"vehicle_id":1}}
# → {"event":"SUBSCRIBED","data":{"vehicle_id":1}}
# → stream VEHICLE_UPDATE / ALERT_NOTIFICATION mengikuti
```

Alternatif uji end-to-end tanpa browser — simulasi device GT06 mengirim telemetri
(tool `backend/loadtest`, frame valid terhadap parser):

```bash
cd backend/loadtest
go run . -host 127.0.0.1:9000 -devices 3 -rate 1 -duration 10s
# default memakai 3 IMEI seed DEV001; override: -imeis "864201040512345,..."
# VEHICLE_UPDATE akan mengalir ke semua WS client berhak dalam < 1 detik
```

---

## 7. Daftar Error Code

Frontend sebaiknya **memetakan `error_code` ke string i18n** (bukan `message`).

### 7.1 Autentikasi & Sesi

| error_code | HTTP | Penyebab | Saran UI |
|---|---|---|---|
| `INVALID_REQUEST` | 400 | Body tidak lengkap/format salah | Validasi form |
| `INVALID_CREDENTIALS` | 401 | Email/password salah | Pesan "Email atau kata sandi salah" |
| `UNAUTHORIZED` | 401 | Token hilang/kedaluwarsa/refresh invalid | Redirect login / auto-refresh |
| `TOKEN_REVOKED` | 401 | Token sudah di-logout (denylist jti) | Paksa re-login |
| `RATE_LIMIT_EXCEEDED` | 429 | Login 5×/15 mnt atau API >100 req/mnt | Tampilkan countdown, throttle |

### 7.2 Otorisasi (RBAC)

| error_code | HTTP | Penyebab |
|---|---|---|
| `FORBIDDEN` | 403 | Role kurang / vehicle-route di luar scope user |
| `PLATFORM_ONLY` | 403 | Endpoint platform dipanggil oleh admin tenant |
| `PLATFORM_ROLE_RESERVED` | 403 | Mencoba assign role `SuperAdmin` via API |
| `PLATFORM_SCOPE` | 403 | Token platform memanggil endpoint non-platform |

### 7.3 Resource

| error_code | HTTP | Penyebab |
|---|---|---|
| `VEHICLE_NOT_FOUND` | 404 | Vehicle tidak ada / sudah dihapus / bukan milik tenant |
| `GEOFENCE_NOT_FOUND` | 404 | Geofence tidak ada / inactive |
| `ALERT_NOT_FOUND` | 404 | Alert id salah |
| `ROUTE_NOT_FOUND` | 404 | Route tidak ada / inactive |
| `ASSIGNMENT_NOT_FOUND` | 404 | Assignment bukan milik route tsb |
| `SPEED_CONFIG_NOT_FOUND` | 404 | Config speed tidak ada / tanpa perubahan |
| `LINK_NOT_FOUND` | 404 | Tautan geofence-vehicle tidak ada |
| `USER_NOT_IN_COMPANY` | 422 | User belum punya `user_company_access` di company ini |
| `DUPLICATE` | 409 | IMEI vehicle sudah terdaftar |
| `ALERT_NOT_UPDATABLE` | 400 | Acknowledge alert yang sudah resolved |
| `INVALID_PARAM` | 400 | Param path/query salah (mis. window history > 30 hari) |
| `INVALID_STATUS` | 400 | Status di luar enum (`active/inactive/maintenance`) |
| `EMPTY_UPDATE` | 400 | PATCH tanpa field apa pun |

### 7.4 Infrastruktur

| error_code | HTTP | Penyebab | Saran UI |
|---|---|---|---|
| `SERVICE_UNAVAILABLE` | 503 | DB/Redis/NATS bermasalah | Retry dengan backoff + banner maintenance |
| `INTERNAL_ERROR` | 500 | Kesalahan tak terduga | Log & laporkan; retry aman untuk GET |
| `PROVISION_FAILED` | 500 | Gagal provision database tenant | Cek log service, coba ulang |

---

## 8. Alur End-to-End Lengkap

Contoh skrip bash siap-copy: dari login → lihat peta live → buka history →
kelola geofence → tangani alert → kelola route.

```bash
#!/usr/bin/env bash
set -euo pipefail
WS=http://localhost:8080   # service-websocket
AV=http://localhost:8081   # api-vehicle

# ---------- 1) LOGIN ----------
TOKEN=$(curl -s -X POST $WS/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@dev001.io","password":"Admin@123"}' | jq -r '.data.token')
AUTH="Authorization: Bearer $TOKEN"

# ---------- 2) LIVE TRACKING ----------
curl -s "$WS/api/v1/vehicles?page=1&limit=50" -H "$AUTH" | jq '.data'
# → render marker peta; update berikutnya lewat WS (§6)

# ---------- 3) HISTORY 30 HARI ----------
FROM=$(date -u -d '7 days ago' +%Y-%m-%dT%H:%M:%SZ)
TO=$(date -u +%Y-%m-%dT%H:%M:%SZ)
curl -s "$WS/api/v1/vehicles/1/history?start=$FROM&end=$TO&limit=10000" \
  -H "$AUTH" | jq '{jumlah: .total_records, titik_pertama: .data[0]}'

# ---------- 4) GEOFENCE BARU ----------
GID=$(curl -s -X POST $WS/api/v1/geofences \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{
        "name": "Zona Ujian QA",
        "area_type": "circle",
        "coordinates": {"type":"Point","coordinates":[106.844,-6.2088]},
        "radius_meters": 1500,
        "vehicle_ids": [1]
      }' | jq -r '.data.id')
echo "Geofence dibuat: $GID"

# ---------- 5) ALERT OPEN + ACK ----------
ALERT_ID=$(curl -s "$WS/api/v1/alerts?status=open&limit=1" -H "$AUTH" \
  | jq -r '.data[0].id // empty')
if [ -n "$ALERT_ID" ]; then
  curl -s -X PATCH "$WS/api/v1/alerts/$ALERT_ID/acknowledge" -H "$AUTH" | jq '.data.status'
fi

# ---------- 6) ROUTE + ASSIGNMENT ----------
# Catatan: di provider postgres, respons create mengembalikan "id": 0 (lihat §2.8).
# Pola aman: create → re-fetch list → ambil id dari item yang cocok.
curl -s -X POST $AV/api/v1/routes \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"name":"Rute QA","waypoints":[{"lat":-6.2088,"lon":106.8456},{"lat":-6.1100,"lon":106.9300}]}' > /dev/null

RID=$(curl -s "$AV/api/v1/routes" -H "$AUTH" \
  | jq -r '[.data[] | select(.name=="Rute QA")][0].id')

curl -s -X POST $AV/api/v1/routes/$RID/assignments \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"vehicle_id":1,"driver_user_id":4}' > /dev/null

AID=$(curl -s "$WS/api/v1/routes/$RID/track" -H "$AUTH" \
  | jq -r '.data.assignments[0].id')

curl -s -X PATCH $AV/api/v1/routes/$RID/assignments/$AID \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"status":"in_progress"}' | jq '.data'
curl -s $WS/api/v1/routes/$RID/track -H "$AUTH" | jq '.data'

# ---------- 7) SPEED CONFIG PER-VEHICLE ----------
curl -s -X POST $AV/api/v1/speed-configs \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"vehicle_id":1,"speed_limit_kmh":60,"grace_margin_kmh":5}' | jq '.data'

# ---------- 8) LOGOUT ----------
curl -s -X POST $WS/api/v1/auth/logout -H "$AUTH" | jq '.data'
```

**Checklist integrasi frontend:**

- [ ] Simpan `token` + `refresh_token` setelah login (httpOnly cookie atau memory + silent refresh).
- [ ] Interceptor 401 → panggil `/auth/refresh` sekali; gagal → redirect login.
- [ ] Semua list handle `pagination` (page/limit/total).
- [ ] Map `error_code` (bukan `message`) ke locale i18n.
- [ ] WebSocket: auto-reconnect exponential backoff + resubscribe vehicle setelah reconnect.
- [ ] Warna status peta: 🟢 MOVING/ONLINE · 🟡 IDLE · ⚫ OFFLINE · 🔴 alert aktif.
- [ ] SOS popup wajib: suara + popup critical + tombol ACK (`PATCH /alerts/:id/acknowledge`).

---

> **Dokumen ini di-generate dari kode sumber** (`controllers/router.go`,
> handler & models kedua service). Saat menambah/mengubah endpoint backend,
> perbarui dokumen ini pada commit yang sama.






---