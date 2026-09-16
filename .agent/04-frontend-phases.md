# 04 — Frontend Phases (Detail)

Prioritas kedua. **Kerjakan HANYA setelah seluruh fase backend B0–B6 selesai** dan contract API/WebSocket stable.

> **Acuan struktur aplikasi (PRD v1.7.0):** modul/menu/fitur mengikuti `docs/FRONTEND.md` —
> Aplikasi **Business** (§1: Utama, Master Data, Akses, Aset & Perawatan, Keamanan,
> Analisis & Laporan, Industry Specific, Administrasi) & **Personal** (§2) + fitur lintas
> aplikasi (§3). F1–F4 di bawah = inti tracking; menu lain dirilis bertahap menyusul
> backend **B12** (PRD §5.10) — menu tanpa backend di-hide/stub.

---

## Phase F1 — Scaffold Frontend

**Tujuan:** Aplikasi Next.js berdiri + integrasi map + koneksi API dasar.

### Tasks
- Scaffold Next.js (App Router) + TypeScript + TailwindCSS.
- Integrasi map: **Mapbox GL** (production) / **Leaflet** (fallback bebas key). Simpan key di env, jangan commit.
- Setup routing, layout, dan komponen base (Sidebar, Navbar) — struktur aplikasi sesuai `docs/FRONTEND.md`: app shell dua aplikasi (Business & Personal) + navigasi menu per grup modul; menu tanpa dukungan backend (B12 ⬜) di-hide/stub. Navigasi menu dimuat dinamis dari `GET /api/v1/access/menu` (B12: katalog `tm_menus` master + akses `tm_role_menu_access` per-tenant); sampai B12 selesai pakai konfigurasi statis dengan struktur yang sama.
- Setup HTTP client (mis. fetch wrapper / axios) dengan:
  - Base URL ke `service-websocket` / `api-vehicle`.
  - Interceptor JWT + penanganan error (format error sesuai backend GAP #3).
- Client WebSocket utility dengan auto-reconnect + exponential backoff (FR-5.3).
- Setup **i18n** (mis. `next-intl`): locale `id` (default) + `en` (Inggris US `en-US`), file `messages/<locale>.json`, deteksi default dari `Accept-Language`, siap ekstensi bahasa lain.

### Acceptance Criteria
- `npm run dev` jalan; peta render; bisa memanggil endpoint health backend.
- Login flow terhubung ke `POST /api/v1/auth/login`.

---

## Phase F2 — Live Tracking Dashboard

**Tujuan:** Peta live + daftar vehicle real-time dengan RBAC.

### Tasks
- Live Tracking Map dengan indikator warna (FR-6.1):
  - 🟢 Bergerak, 🟡 Berhenti, ⚫ Offline, 🔴 Alert/Geofence.
- Real-time list vehicle: status, lokasi, kecepatan (FR-6.2).
- Subscribe WebSocket `vehicle.update.<vehicle_id>` sesuai hak akses user (RBAC).
- Query performance: daftar vehicle user < 1 detik.

### Acceptance Criteria
- Pergerakan vehicle tampil "smooth" (< 800ms end-to-end dari device).
- Hanya vehicle milik user yang tampil.

### F2.1 Admin Monitoring / Observability (live-time, ADMIN only)

**Tujuan:** Dashboard pemantauan sistem real-time khusus role **ADMIN** (/langkah opsional untuk adatrack_MANAGER bila disetujui) — memanfaatkan metrik infra yang disiapkan di backend B4 (CPU, Memory, RDS).

**Sumber data:**
- Polling berkala (~5–10 detik) ke **Prometheus HTTP API** (`/api/v1/query`) ATAU agregasi dari `/metrics` tiap service via endpoint aggregator di service-websocket (pilih kontrak yang disepakati di B4).
- Semua permintaan melalui proksi ber-autentikasi JWT + cek role di sisi server (bukan akses Prometheus langsung dari browser).

**Widget yang ditampilkan (live):**
- **CPU & Memory:** host (`node_exporter`) & container (`cAdvisor`) — usage presentase + tren grafik.
- **DB PostgreSQL:** `pg_stat_database_numbackends` vs `max_connections`, slow queries (`pg_stat_statements`), buffer cache hit ratio, write latency, status partition — selaras metrik infra B4 §4.1 & PRD §20.2 (F2.1 "DB PostgreSQL widgets").
- **Layanan:** koneksi WebSocket aktif, NATS pending messages, error rate, latensi batch insert (memakai metrik PRD §8.1 yang sudah ada).

**RBAC & akses:**
- Route/halaman ini **terproteksi**: hanya role **ADMIN** yang bisa melihat di UI (guard di frontend + validasi role di endpoint/WS).
- Non-admin → menu tidak tampil; akses langsung ke endpoint → 403.

**Integrasi grafis (optional):**
- Widget tabular/sparkline dengan library chart (mis. Recharts) ATAU embed dashboard **Grafana** (iframe) GET khusus admin.

### Acceptance Criteria
- Admin dapat melihat metrik CPU, Memory, RDS yang ter-refresh dalam < 10 detik.
- Non-admin tidak melihat halaman ini dan ditolak (403) bila akses langsung.

---

## Phase F3 — History Playback, Geofence & Alerts UI

**Tujuan:** Fitur historis & manajemen zona/alert.

### Tasks
- **History Playback** (FR-6.3): jalur 24 jam, timeline scrubber, play/pause/stop, kontrol kecepatan (1x/2x/5x/10x).
- **Geofence Management** (FR-6.4): create/edit/delete zona (circle/polygon) + set alert rules.
- **Alerts UI:** list alert, acknowledge, visual marker merah di peta.
- **SOS UI (darurat):** tampilan mencolok (popup + suara + marker merah di peta), panel detail (driver/vehicle/lokasi/timestamp), ACK → resolved, tampilkan timer respons (TTA).

### Acceptance Criteria
- Playback history 30 hari dimuat < 1.5 detik.
- Buat geofence → vehicle breach → muncul alert di peta & list.

---

## Phase F4 — Auth/UX, Reporting & Polish

**Tujuan:** Pengalaman end-user lengkap.

### Tasks
- Perfeksi alur auth (refresh token, session, role-aware UI) sesuai role ADMIN/adatrack_MANAGER/OPERATOR/DRIVER.
- Reporting/analytics dasar untuk adatrack Manager (ringkasan trip, violation summary; export PDF/Excel bila disetujui).
- Responsive design, loading/skeleton, empty state, error state.
- Polish: performa bundle, aksesibilitas, styling konsisten Tailwind.

### F4.1 Multi-Bahasa (i18n, extensible)

**Tujuan:** UI mendukung bahasa Indonesia (default) & Inggris US — dan **mudah menambahkan bahasa lain**.

- Daftar bahasa aktif minimum: `id` (default) & `en-US` (Inggris US).
- **Language switcher** di UI (Sidebar/Navbar): ID / EN-US; simpan preferensi user (persistent, jangan hardcode).
- Pesan error/alert dari backend (GAP #3 `error_code`) dipetakan ke string per locale **di frontend**, supaya tampil dalam bahasa yang sesuai.
- Format waktu/tanggal & angka mengikuti locale (mis. `id-ID`, `en-US`).
- **Ekstensi bahasa baru:** cukup tambah file `messages/<locale>.json` + registrasi locale. Semua string UI harus memakai key i18n (bukan hardcoded) agar mudah di-terjemahkan.
- Konten dinamis dokumen (alert severity, nama field, label) juga lewat i18n key, bukan string tersebar.

**Acceptance Criteria (F4.1):**
- UI dapat beralih antara ID ↔ en-US; preferensi tersimpan.
- Tidak ada string user-facing yang di-hardcode di komponen.
- Visibilitas: memuat bahasa baru (mis. `ms`, `es`) hanya dengan menambah file terjemahan + registrasi.

### Acceptance Criteria
- Semua role mendapat tampilan sesuai hak akses.
- Dashboard stabil untuk 5000+ vehicle real-time & 5000+ user.

---

## Definisi "Selesai" untuk Keseluruhan Project

- Semua **success criteria di `PRD.md §12`** terpenuhi.
- Backend: 6 service berjalan, 2000 msg/sec tanpa data loss, query historis < 1.5s.
- Frontend: dashboard real-time untuk 5000+ vehicle, playback, geofence, alert.
- Monitoring + alerting aktif; test coverage ≥ 80%.