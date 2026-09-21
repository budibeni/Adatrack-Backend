# 02 — Roadmap Overview (Phase Plan)

Prinsip: **Backend diselesaikan dulu secara berurutan, lalu Frontend.**

> **PROGRESS 2026-09-19:** **B0 ✅**, **B1 ✅**, **B2 ✅**, **B3 ✅**, **B5a ✅** selesai,
> dan **B4 🟡 sebagian** (performance/monitoring/hardening/DR terverifikasi nyata —
> load 400/1000/2000 msg/s **0 data loss**, SLA query 24–32 ms, Prometheus+Grafana+20 rule,
> backup/restore drill row-count match, retensi partisi; **gap**: coverage ≥80% service inti,
> endurance 24 jam penuh, drill replika). Bukti: `docs/B4-VERIFICATION.md`.
> Fase berikutnya: **B5b / B6 / B7**.
> (compose/migrations/`internal`/`foundation-check` + pipeline ingestion-tcp →
> worker-live → worker-persistence: load 1000 msg/s tanpa data loss, isolasi
> tenant 0 leakage, unit+integration test hijau; **service-websocket**: login
> JWT+refresh rotation, RBAC row-level, REST + WS, FR-5.5 auto-provision,
> audit trail — `make e2e-ws` **21/21 PASS** dengan WS push **8 ms**,
> provisioning FR-5.5/FR-5.6 **31/31 PASS**; **B3**: worker-alert — geofence
> circle/polygon, overspeed grace band, SOS critical + eskalasi, battery/offline,
> route deviation, notifikasi preferensi + fan-out; api-vehicle — CRUD
> vehicles/geofences/routes/assignments/speed-configs + soft delete/restore +
> RBAC row-level; `scripts/test.sh` exit 0 semua modul; live DB drift diperbaiki
> (commit `408c241`: migrasi `020_repair_platform_admin` + `013_repair_dev_rbac`,
> kedua kredensial dev login 200, ledger 0 failure). Fase berikutnya: **B5a / B4**.
> Checklist & bukti: `.agent/03-backend-phases.md`.

> **Acuan struktur aplikasi (PRD v1.7.0):** penerapan backend mengikuti
> `docs/FRONTEND.md` — modul/menu/fitur aplikasi **Business** & **Personal** (§5.10
> PRD, Module 9); fase backend pendukungnya = **B12** (⬜ Planned).

## Macro Sequence

| Fase | Area | Lokasi | Status |
|---|---|---|---|
| **B0** | Infrastruktur + Foundations | `backend/` | ✅ Selesai 2026-09-15 |
| **B1** | Pipeline Data: ingestion-tcp · worker-live · worker-persistence | `backend/services/ingestion-tcp`, `backend/services/worker-live`, `backend/services/worker-persistence` | ✅ Selesai 2026-09-15 |
| **B2** | service-websocket (REST API + WebSocket + RBAC) | `backend/services/service-websocket` | ✅ Selesai 2026-09-15 |
| **B3** | Alerts, Geofence, & API Vehicle | `backend/services/worker-alert`, `backend/services/api-vehicle` | ✅ Selesai 2026-09-16 (live DB drift diperbaiki, migrasi `020`+`013`) |
| **B5a** | Fuel Sensor End-to-End (PRD v1.3.0 Module 7) | `ingestion-tcp`, `worker-live`, `worker-persistence`, `worker-alert`, `api-vehicle` | ✅ Selesai (unit + REST overlay; E2E live fuel menyusul) |
| **B5b** | Dashcam Event Media — Scope A (PRD v1.3.0 Module 8) | `backend/services/service-media`, `internal/storage`, bridge `service-websocket` | ⬜ Belum dimulai |
| **B4** | Performance, Monitoring, Testing, Hardening | `backend/` | 🟡 Sebagian (2026-09-19) — load 400→2000 msg/s 0 loss, SLA query, monitoring stack + rule SLO/alert, backup/restore drill, retensi; **gap**: coverage ≥80% service inti, endurance 24 jam penuh, drill replika, load WS 50×1200. Bukti: `docs/B4-VERIFICATION.md` |
| **B6** | Real-Time Data Hardening (Audit Fix) | `service-websocket` | ⬜ Belum dimulai |
| **B7** | Fleet Management Core (B7.1 Odometer & Engine Hours · B7.2 Trip & Stop Detection · B7.3 Reverse Geocoding · B7.4 Point Reduction) | `worker-live` (+ migrasi company) | ⬜ Belum dimulai |
| **B8** | Advanced Fleet Features (downlink/remote commands `DYD#`, driver behavior, maintenance scheduling) | `ingestion-tcp`, `worker-alert` | ⬜ Planned |
| **B9** | Protocol Expansion (Meiligao, Xexun, Suntech, H02, Totem, GT02, Navigil, Castel; validasi TK103) — port & decoding per referensi Traccar | `ingestion-tcp` | ⬜ Planned |
| **B10** | **Normalisasi & Konfigurasi** — prefix tabel `tm_`/`th_`/`td_` (migrasi rename idempoten), split user master `tm_users` (B2B) / `tm_users_b2c` (B2C), `business_type` di `tm_companies`, config ganda LOCAL + COOLIFY (`docker-compose.{local,coolify}.yml` + `.env.{local,coolify}`), telemetry interval 20 s, input validation + anti-attack hardening (§8.5/§9.6) | `backend/`, `database/migrations` | ⬜ Planned |
| **B11** | **Governance & Data Lifecycle** — audit trail wajib `tm_audit_logs` (§9.4), soft delete global + endpoint restore (§6.0.1), auto-create admin tenant `Admin@123` (FR-5.5), migrasi DB otomatis Coolify (§14.5), dukungan protokol universal (Module 1c) | `backend/`, `deployments/` | ⬜ Planned |
| **B12** | **Enterprise & Industry Modules** (acuan `docs/FRONTEND.md`, PRD §5.10 Module 9) — drivers, groups, personel/kartu RFID/log akses, assets, maintenance, safety score & incidents, laporan/analitik lanjutan, organization, integrations (API/Webhook), share lokasi publik, heatmap; modul industry-specific (rental, transport, logistics, sales, field service, patrol, project site) bertahap; Personal/B2C; registry module & menu master (`tm_modules`/`tm_menus`) + role menu access per-tenant (`tm_role_menu_access`) | `backend/` | ⬜ Planned |
| **F1** | Scaffold Frontend (Next.js + Tailwind + Map) | `frontend/` | ⬜ Not started |
| **F2** | Live Tracking Dashboard | `frontend/` | ⬜ Not started |
| **F3** | History Playback, Geofence, Alerts UI | `frontend/` | ⬜ Not started |
| **F4** | Auth/UX, Reporting, Polish | `frontend/` | ⬜ Not started |

> PENTING: Fase **frontend (F1–F4) TIDAK boleh dimulai** sampai seluruh fase backend (B0–B6) **selesai** dan terverifikasi berjalan (infra up, pipeline data live, API + WebSocket berfungsi). Selaras PRD §20.2: **B7–B12 berlanjut paralel dan tidak memblokir frontend** (B7 optional continuing).

## Fitur Lintas-Fase: Multi-Bahasa (i18n)

- **Setup** di **F1** (scaffold `next-intl`, locale `id` + `en-US`).
- **Penyempurnaan + ekstensibilitas** di **F4.1** (switcher, mapping error ke locale, alur menambah bahasa lain).
- Prinsip: semua string UI memakai **i18n key** (bukan hardcode) agar Bahasa Indonesia (default) & Inggris US tersedia, dan bahasa lain (mis. `ms`, `en`, `zh`) cukup ditambahkan file terjemahan.

## Keputusan Fitur Lintas-Fase: Notifikasi, Route & SOS

- **Notifikasi (harus ada, bisa diset):** pengiriman alert via WebSocket/email/SMS, dikonfigurasi per user/alert lewat `notification_preferences` (aktif/nonaktif + pilih channel). Backend di **B3** (worker-alert + `notification_preferences`), UI di **F3/F4**.
- **Route (harus ada — untuk mendisiplinkan driver):** buat & assign rute ke driver, lacak status & deteksi penyimpangan. Backend di **B3** (`routes`), UI di **F3** (assignment & penyimpangan). Tidak semua user butuh, tapi fitur wajib tersedia & bisa dikonfigurasi.
- **SOS (harus ada, prioritas tinggi):** deteksi event SOS → alert CRITICAL + lokasi + WebSocket real-time, life-cycle ACK/resolved, eskalasi otomatis bila tak di-ACK, catat TTA. Backend di **B3** (`alert.sos.<IMEI>`), UI di **F3** (popup + detail + ACK).
- **Sensor bahan bakar (harus ada — enterprise, PRD v1.3.0 Module 7):** kanal fuel-level real-time (GT06 `0x0D` + Teltonika AVL IO), persistensi `fuel_logs`, alert FUEL_DROP/REFUEL. Backend di **B5a**, UI (grafik BBM + riwayat) di **F3**.
- **Dashcam event media (scope A — enterprise, PRD v1.3.0 Module 8):** snapshot/clip saat SOS/alarm/manual → MinIO/S3 → katalog ber-RBAC → WS `MEDIA_EVENT`. Backend di **B5b**, UI (galeri media) di **F3**. Live streaming out-of-scope fase ini.
- **Provider database (diperbarui 2026-09-15):** PRD konsolidasi v1.6.0 §7.1 menetapkan **PostgreSQL sebagai satu-satunya engine persisten** (schema-per-tenant); `DATABASE_PROVIDER` tidak ada di daftar env PRD. Seluruh jalur persistensi — migrasi `master_pg`/`company_pg`, bootstrap `init-pg`, compose `postgres:15-alpine` + overlay replika streaming WAL, replikasi/backup — menargetkan PostgreSQL. Catatan implementasi & limitasi porting SQL: `docs/POSTGRES_PROVIDER.md`.

## Referensi Protokol

### Teltonika Codec 8 Extended (STANDALONE — PRIORITAS TINGGI)

> **📚 Teltonika Codec 8 Extended Protocol Reference:** `docs/docs-device/traccar-reference/08-teltonika-codec8.md`
>
> Dokumen **standalone** untuk referensi protokol Teltonika. Dipisahkan dari referensi
> protokol lainnya karena Teltonika adalah **protokol utama yang didukung** di project ini
> (diimplementasi di fase B5a — Fuel Sensor End-to-End).
>
> **Prioritas codec dalam keluarga Teltonika:**
> | Prioritas | Codec | ID | Status |
> |-----------|-------|----|--------|
> | **HIGHEST** | **Codec 8 Extended** | `0x8E` | ✅ Diimplementasi |
> | HIGH | Codec 8 | `0x08` | ✅ Diimplementasi |
>
> **Catatan penting:** Codec 8 Extended (0x8E) mempunyai prioritas **lebih tinggi** dari
> device Teltonika lainnya karena mendukung IO ID space yang lebih besar (2-byte = 65535 IDs
> vs 255), diperlukan untuk fitur advanced (fuel sensors, extended telemetry), dan digunakan
> oleh device modern FMB/FMM family.
>
> Dokumentasi mencakup:
> - Struktur frame TCP/UDP (preamble, length, payload, CRC-16/IBM)
> - Format AVL record (base 28 byte untuk 0x8E PRIMARY, 26 byte untuk 0x08 SECONDARY)
> - Struktur IO elements (1-byte, 2-byte, 4-byte, 8-byte, variable length)
> - Daftar lengkap IO IDs (battery, ignition, fuel, GPS, GSM, dll)
> - Contoh packet hex dan kode implementasi Go

### Traccar Protocol Reference (Protokol Lainnya)

Untuk ekspansi protokol di luar GT06/Teltonika/TK103 yang sudah diimplementasikan:

> **📚 Traccar Protocol Reference:** `docs/docs-device/traccar-reference/`
>
> Dokumentasi lengkap 200+ protokol GPS dari Traccar (open-source platform).
> Lihat `PRD.md` Module 1d untuk detail prioritas implementasi.

## Dependencies Antar Fase

- **B0** wajib lebih dulu (tanpa infra & schema, service lain tak bisa jalan).
- **B1** butuh B0 (NATS/PostgreSQL/Redis + schema).
- **B2** butuh B0 (schema users/vehicles) + B1 (data di PostgreSQL & Redis).
- **B3** butuh B1 (aliran telemetry + live state) + B2 (RBAC untuk mapping & notifikasi).
- **B5a** (Fuel Sensor, PRD v1.3.0) butuh B1 (pipeline telemetry) + B3 (pola alert/notifikasi); dapat berjalan sebelum/paralel dengan B4.
- **B5b** (Dashcam Event Media, PRD v1.3.0) butuh B0 (+MinIO di compose), B2 (RBAC/JWT interop), B3 (trigger capture dari alert critical); dapat berjalan sebelum/paralel dengan B4.
- **B4** butuh B1–B3 (optimasi pada pipeline yang sudah jalan).
- **B6** (Audit Fix) butuh B2 (WebSocket bridge) + B5a (fuel data di live-state); effort kecil, dampak tinggi pada akurasi data real-time.
- **B7** (Fleet Management Core) butuh B1 (pipeline telemetry + live state) + B3 (geofence/alert patterns); Odometer & Engine Hours (B7.1) + Trip & Stop Detection (B7.2) sub-fase selesai 2026-09-11; Reverse Geocoding & Point Reduction dapat berjalan setelah B1 stabil.
- **B8–B11** berjalan berlapis di atas pipeline & schema yang sudah stabil (setelah B7); urutan logis: **B10 mendahului B11** (normalisasi schema `tm_`/`th_`/`td_` + config ganda dulu, baru governance/audit/soft-delete/migrasi otomatis); **F1–F4 tetap tidak diblokir** B7–B11 (gate frontend tetap B0–B6).
- **B12** (Enterprise & Industry Modules, acuan `docs/FRONTEND.md`/PRD §5.10) berjalan setelah/bersama B8–B11 — sifat **additive** (endpoint + tabel baru, tanpa mengubah pipeline/core); maintenance menyambung B8, safety score menyambung driver behavior B8.
- **F1–F4** butuh **B2** (REST + WebSocket + RBAC sudah stable) sebagai contract API.

## Aturan Transisi Fase

1. Selesaikan satu fase dari awal sampai *acceptance criteria* terpenuhi.
2. Verifikasi secara lokal: build sukses, service boot, endpoint `/healthz` OK, data mengalir (untuk fase yang menyentuh pipeline).
3. Update status di file ini (`⬜` → `✅`) setelah fase disetujui.
4. Jangan loncat ke fase berikutnya bila fase berjalan masih ada blokir yang belum selesai.