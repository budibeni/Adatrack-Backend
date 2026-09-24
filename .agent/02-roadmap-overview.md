# 02 — Roadmap Overview (Phase Plan)

Prinsip: **Backend diselesaikan dulu secara berurutan, lalu Frontend.**

> **PROGRESS 2026-09-22:** **B0 ✅**, **B1 ✅**, **B2 ✅**, **B3 ✅**, **B5a ✅**, **B5b ✅** selesai,
> dan **B4 🟡 sebagian** (performance/monitoring/hardening/DR terverifikasi nyata —
> load 400/1000/2000 msg/s **0 data loss**, SLA query 24–32 ms, Prometheus+Grafana+20 rule,
> backup/restore drill row-count match, retensi partisi; **gap**: coverage ≥80% service inti,
> endurance 24 jam penuh, drill replika). Bukti: `docs/B4-VERIFICATION.md`.
> **B4 ✅ TUNTAS (2026-09-24):** **endurance 24/24 chunk PASS** (±1.436.000 pesan/chunk
> @400 msg/s, 0 loss/chunk, 0 `backpressure DROP`), **drill replika 21/21** (kini self-healing:
> deteksi replika belum menyusul → seed ulang base backup; teruji saat slot menahan 13,67 GB WAL),
> coverage service inti ≥ 80 % (internal 91,9 · wp 91,1 · wl 86,8 · wa 84,2 · apiv 80,1),
> indeks `idx_timestamp` FR-3.5 dipulihkan (SLA 30 hari 5.952 ms → **12 ms** pada bentuk
> endpoint), multi-tenant lulus (race at-most-once diperbaiki), kapasitas JetStream diset
> 16 GiB/stream + server 120GB. Catatan jujur: probe `count(*)` **global** (tanpa filter
> kendaraan) kini dilaporkan `[INFO]` — bukan SLA PRD karena tak ada endpoint yang
> memanggilnya. Sisa pekerjaan B4: **tidak ada item merah**; lihat §2.15.
> **B5b (2026-09-22):** `internal/storage` (S3 SigV4 stdlib + Mem), service baru
> `services/service-media` (HMAC ingest multipart/JSON+presigned PUT, katalog `th_media_events`
> ber-RBAC, presigned GET + audit fail-closed `MEDIA_URL_ACCESS`, soft delete/restore, retensi
> cron + `HARD_DELETE`), bridge `service-websocket` (`MEDIA_EVENT` + `notify.alert.<vehicle_id>`),
> wiring compose/env/Makefile/prometheus, migrasi additive `016_media_events_governance`.
> **Audit ulang 2026-09-22 (setelah commit pertama):** 2 temuan diperbaiki — (a) `main.go`
> service-media belum mengkabel Redis sehingga denylist revokasi JWT + limiter + cek Redis di
> `/healthz` no-op, (b) tier ingest HMAC belum ber-rate-limit. Keduanya kini aktif & terverifikasi
> live (429 saat flood, 401 `TOKEN_REVOKED` setelah logout, `/healthz` 503 saat MinIO mati).
> **B5a dituntaskan:** kalibrasi `FUEL_TANK_HEIGHT_CM` kini diterapkan di ingestion +
> flusher fuel-only worker-persistence diperbaiki. E2E live: `make e2e-fuel` **11/11 PASS** dan
> `make e2e-media` **18/18 PASS** (MinIO/PostgreSQL/Redis/NATS nyata) — checklist: `.agent/03-backend-phases.md`.
> **PROGRESS 2026-09-24:** **B6 ✅** dan **B7 ✅** (B7.1–B7.4) selesai.
> **B6 (audit fix):** audit menemukan inferensi ACC yang tersisa — `acc` dipublikasikan `bool` +
> `omitempty`, sehingga frame TANPA ACC (fuel-only `!AIOIL`, alarm LBS 0x19, Teltonika tanpa IO
> ignition) tetap terkirim sebagai `acc:false`. ACC kini **tri-state `*bool` end-to-end**
> (ingestion → live state → `acc_status` NULL via migrasi `020` → DTO WS/playback), dan DTO
> FR-5.2 (`fuel_level/fuel_volume/fuel_temp_c/satellites/altitude/gsm_signal`) dikunci test.
> **B7 (fleet core):** migrasi `018` (odometer/engine hours + CHECK anti-rollback) & `019`
> (`th_vehicle_trips`/`td_vehicle_stops`), akumulator Haversine + guard FR-2.5 (GPS jump >5 km,
> gap >300 s, fuel-only/heartbeat, VehicleID=0) dengan engine hours hanya saat ACC ON, state
> machine trip/stop FR-2.6 (grace 30 s, min stop 60 s, auto-close 3600 s), reverse geocoding
> offline (cache in-memory + Redis + indeks master, fallback `resolved=false`), dan point
> reduction RDP pada endpoint baru `GET /vehicles/{id}/playback` + `GET /geocode/reverse`.
> **E2E:** `make e2e-fleet` **10/10 PASS** (odometer 0.334 km = rute, GPS jump dibuang, trip
> 0.222 km/60 s dengan 1 stop 130 s, playback 21→2 titik + alamat).
> **Gap yang dicatat jujur:** presisi geocoding berhenti di level kota (seed wilayah tanpa
> koordinat kecamatan/desa) & metrik B7 belum masuk dashboard B4.
> Bukti: `docs/B6-B7-VERIFICATION.md`; checklist: `.agent/03-backend-phases.md`.
> **Verifikasi pihak ketiga (sesi B4, 2026-09-24):** kode B6/B7 yang di-commit (`10d7fff`)
> diperiksa ulang: `go build ./...` OK untuk 7 modul (internal, ingestion-tcp, service-websocket,
> worker-live, worker-persistence, worker-alert, tools/e2e-fleet), `bash -n scripts/e2e-fleet.sh` OK,
> dan **`scripts/test.sh` (ADATRACK_IT=1) selesai dengan 0 FAIL** untuk seluruh modul.
> Fase berikutnya: **B8 / B9 / B10 / B11** (B10 mendahului B11), lalu **B12**.
> (Catatan: `make e2e-fleet` memakai plan gerak sintetis + memulihkan counter kendaraan fixture.)
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
| **B5b** | Dashcam Event Media — Scope A (PRD v1.3.0 Module 8) | `backend/services/service-media`, `internal/storage`, bridge `service-websocket` | ✅ Selesai 2026-09-22 (`make e2e-media` 18/18 PASS) |
| **B4** | Performance, Monitoring, Testing, Hardening | `backend/` | 🟡 Sebagian (2026-09-19) — load 400→2000 msg/s 0 loss, SLA query, monitoring stack + rule SLO/alert, backup/restore drill, retensi; **gap**: coverage ≥80% service inti, endurance 24 jam penuh, drill replika, load WS 50×1200. Bukti: `docs/B4-VERIFICATION.md` |
| **B6** | Real-Time Data Hardening (Audit Fix) | `service-websocket` |✅ Selesai 2026-09-24 — ACC **tri-state** (`bool` → `*bool`: `acc_status` NULL, key `acc` hilang saat device tidak melaporkan), DTO FR-5.2 lengkap dikunci test, REST/WS sesuai data device. Bukti: `docs/B6-B7-VERIFICATION.md` §1 |
| **B7** | Fleet Management Core (B7.1 Odometer & Engine Hours · B7.2 Trip & Stop Detection · B7.3 Reverse Geocoding · B7.4 Point Reduction) | `worker-live` (+ migrasi company) | ✅ Selesai 2026-09-24 — migrasi `018`/`019`/`020`, akumulator FR-2.5 (Haversine + guard jump/gap), state machine FR-2.6 (`th_vehicle_trips`/`td_vehicle_stops`), geocoding offline + RDP playback; `make e2e-fleet` **10/10 PASS** (odometer 0.334 km = rute, trip/stop sesuai rencana). Gap: presisi geocoding berhenti di level kota (seed wilayah tanpa koordinat kecamatan/desa) — `docs/B6-B7-VERIFICATION.md` §3 |
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