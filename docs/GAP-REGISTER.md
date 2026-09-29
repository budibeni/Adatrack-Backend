# GAP REGISTER — B0…B12 (audit jujur, 2026-09-29)

> **Pernyataan kejujuran.** Dokumen ini memisahkan tiga hal yang sering dicampur:
> **A. Defect** (kode/DB/deploy salah → wajib diperbaiki untuk produksi),
> **B. Ketidakakuratan dokumentasi** (dokumen menjanjikan X, kode melakukan Y),
> **C. Scope/roadmap** (fitur yang belum dibangun — *bukan* defect; ini pekerjaan produk).
>
> Target “B0–B12 tanpa GAP sama sekali” **tidak dapat dicapai dalam satu sesi**:
> kategori **C** mencakup pekerjaan produk berbulan-bulan (±45 halaman modul industri,
> B2C, frontend F1–F4, varian protokol tambahan). Yang **dapat** dan **sudah** dituntaskan
> adalah kategori **A** dan **B** pada cakupan yang terukur, dengan bukti runtime.

## 0. Ringkasan

| Kategori | Jumlah | Selesai | Terbuka |
|---|---|---|---|
| A. Defect (produksi) | 9 | **9** | 0 |
| B. Dokumentasi tidak akurat | 4 | 4 | 0 |
| C. Scope/roadmap (bukan defect) | 10 | 0 | 10 (dijadwalkan) |

**Verdict produksi (kondisi kini):** **LAYAK untuk backend B2B** — seluruh pipeline,
REST, WebSocket, alert, media, fleet, governance berjalan & terverifikasi E2E live;
deploy path sudah dikunci anti-drift. **Belum lengkap** untuk klaim “semua fitur B0–B12”
(kategori C) dan **tanpa frontend** (F1–F4 belum dimulai).

---

## A. DEFECT (kode/DB/deploy) — wajib untuk produksi

| # | Defect | Dampak | Status | Bukti |
|---|---|---|---|---|
| A1 | **Schema drift**: kode sampai B12, DB dev di master 020/company 025 | endpoint B11/B12 → 404; IT test 10/10 FAIL | ✅ **FIXED** | `docs/B11-B12-VERIFICATION.md §4b.1` |
| A2 | **`start-services.sh` tidak menjalankan migrasi** (host dev) | service bisa boot melawan schema basi — akar A1 | ✅ **FIXED** | `§4b.2` + validasi live |
| A3 | **`migrate.sh` hanya migrasi default+dev001** | tenant ke-3 `adatrack_gps_loadt2` **tertinggal 10 migrasi** | ✅ **FIXED** | `§4b.3` (loadt2 → 027) |
| A4 | **Tidak ada harness E2E B12** (`e2e-enterprise.sh`) | B12 hanya terbukti unit+SQL, bukan HTTP live | ✅ **FIXED** | `scripts/e2e-enterprise.sh` + `make e2e-enterprise` → **31/31 PASS** |
| A6 | **`GET /audit-logs` & `POST /share-links` selalu 503** di provider Postgres: `entity_id` NULL di-scan ke `string`; `vehicle_ids` `bigint[]` di-scan ke `[]int64` | 2 endpoint B11/B12 **mati total** saat dipakai (401 tanpa token menyembunyikannya) | ✅ **FIXED** | scan nullable + `vehicle_ids::text` + `parseInt64Array`; IT regression `it_share_audit_test.go` |
| A7 | **`vehicleStoreErr` menelan error** (`_ = err`) → 503 generik tanpa jejak di log. Termasuk pelanggaran aturan global “jangan silent drop”. | Mustahil mendiagnosa 503 (menyembunyikan A6 selama berbulan-bulan) | ✅ **FIXED** | kini `slog.Error("store error", …)`; error A8 di bawah ditemukan **karena fix ini** |
| A8 | **`SharedVehicles` memfilter kolom yang tidak ada**: `WHERE company_code = $1` pada `tm_vehicles` (schema-per-tenant **tidak** punya kolom itu) → `SQLSTATE 42703` → `GET /share/{token}` publik **503** | share lokasi publik (FR-9.3) tidak berfungsi | ✅ **FIXED** | predikat dihapus (pool sudah ter-scope schema); IT regression `TestITStoreSharedVehicles` |
| A9 | **`tm_integrations.events` (`text[]`) di-scan ke `[]string`** — kelas yang sama dengan A6; `GET /integrations` akan 503 begitu ada satu baris integrasi | integrasi API/Webhook mati saat dipakai | ✅ **FIXED** | `events::text` + `parseStringArray`; IT regression `TestITStoreIntegrationEventsArray` + cek live di `e2e-enterprise.sh` (35/35) |
| A10 | **Race registrasi pending vs ACK** (jalur B8): `dispatch()` menulis frame ke socket **sebelum** mendaftarkan command di `pending`, sehingga balasan `0x21` yang tiba saat penulisan dicap *"unsolicited"* dan dibuang → baris tetap `sent` → 30 s kemudian `timeout`. Terlihat sebagai `e2e-commands` flaky. | command yang sudah di-ACK device tercatat gagal/timeout | ✅ **FIXED** | (1) daftarkan `pending` **sebelum** `Write` (bersihkan bila write gagal); (2) upsert status **monotonik** — `sent` tak boleh menimpa `acked/failed/timeout` (juga melindungi dari **redelivery durable**). Bukti: `commanddispatch_race_test.go` — **GAGAL dengan urutan lama, PASS dengan fix** (dibuktikan dengan revert sementara) |
| A5 | **FR-4.1 delivery durable vs pipeline telemetri *core NATS* (at-most-once)** | pesan yang dipublikasikan saat worker mati **hilang** | ✅ **FIXED (standard enterprise, 2026-09-29)** | Semua worker + bridge WS + dispatcher command kini memakai **durable PULL consumer** (Ack/Nak, `ensureStreams` pre-create dengan `DeliverNewPolicy` → lalu **BIND**, sehingga backlog downtime diproses tanpa replay riwayat). Bukti live: **10/10 pesan saat downtime dipulihkan** (sebelumnya 0 = hilang) + `e2e-fleet` 10/10. 3 jebakan nyata ditemukan & didokumentasikan di PRD `FR-4.1a`: (1) consumer **push** dihapus saat unsubscribe → restart selalu membuat consumer baru (skip backlog); (2) meminta `MaxAckPending` berbeda DITOLAK → jalur B8 diam-diam turun ke core NATS; (3) `FilterSubject` harus sama persis dengan subjek langganan |

### A5 — detail & rekomendasi (jujur)
- Jalur **downlink command** (B8) **sudah** memakai *durable JetStream consumer* (terbukti:
  `e2e-commands` memverifikasi command yang dipublikasikan saat service MATI tetap terkirim).
- Jalur **telemetri** (`telemetry.raw.*`) memakai `conn.QueueSubscribe` (core NATS).
  Untuk data posisi 5 detik yang di-batch, kehilangan sesaat *boleh* ditoleransi
  (device akan kirim titik berikutnya); **tetapi** PRD menuliskan durable.
- **Rekomendasi (pilih satu, butuh keputusan pemilik produk):**
  1. **Terima deviasi** → perbarui PRD §FR-4.1 (didukung fakta: telemetri idempoten-per-titik,
     command sudah durable) — biaya rendah, tanpa risiko regresi; **ATAU**
  2. **Migrasi pipeline ke JetStream durable** → perubahan arsitektur signifikan
     (ack/NACK per pesan, redelivery, perubahan `internal/natsclient` + 3 consumer),
     berisiko pada throughput 2000 msg/s yang sudah terverifikasi 0-loss.

---

## B. KETIDAKAKURATAN DOKUMENTASI — diperbaiki (dokumen, bukan kode)

| # | Klaim dokumen | Realita (terverifikasi) | Status |
|---|---|---|---|
| B1 | `scripts/test.sh` (ADATRACK_IT=1) 0 FAIL | `test.sh` **tidak** men-set `ADATRACK_IT=1` → IT di-skip; ADATRACK_IT=1 dulu 10/10 FAIL | ✅ dikoreksi di `.agent/02-roadmap-overview.md` |
| B2 | “5 endpoint B11/B12 → 404” | **salah probe saya** (path tebakan); path benar 18/18 = **401** | ✅ dikoreksi di `B11-B12-VERIFICATION.md §4b` |
| B3 | B3/B4: api-vehicle punya `POST /api/v1/auth/login` (+refresh/logout) | **api-vehicle TIDAK punya route auth apa pun** (:8081/login → 404); satu-satunya otoritas auth = **service-websocket** (:8082 login 400/refresh 400/logout 401 = route ada), api-vehicle hanya memverifikasi JWT | ✅ dikoreksi (`.agent/03-backend-phases.md` + register ini) |
| B4 | “6 stream JetStream” | **7** stream (telemetry-raw/live/error, alert, notify, media, command) | ✅ dikoreksi di `B11-B12-VERIFICATION.md §4b.4` |

> **Catatan penting B3:** kode **tidak** salah — memusatkan auth di service-websocket
> lebih aman daripada dua endpoint login paralel. Yang diperbaiki adalah **dokumen**,
> bukan menambah endpoint login duplikat di api-vehicle.

---

## C. SCOPE / ROADMAP — belum dibangun (*bukan* defect)

Diurutkan menurut dampak produksi. Semua item di bawah **secara arsitektur sudah
disiapkan** (skema/registry/pola ada); yang tersisa adalah **volume** pekerjaan.

| # | Item | Kenapa bukan defect | Estimasi |
|---|---|---|---|
| C1 | **B12 · halaman modul industri** (±45: rental/transport/logistics/sales/field-service/patrol/project-site) | registry modul + lisensi + gating menu **sudah ada**; ini CRUD per sub-halaman (produk) | besar |
| C2 | **B12 · Personal/B2C (FR-9.2)** | `tm_users_b2c` sudah ada (B10); auth+endpoint B2C = fitur baru | besar |
| C3 | **B12 · export laporan** — CSV **✅ dikerjakan 2026-09-29** (`/reports/trips/export`, `/reports/violations/export`); PDF & laporan terjadwal masih terbuka | ringkasan on-demand sudah ada | sedang |
| C4 | **B12 · Settings tenant** | preferensi per-tenant belum ada endpoint | sedang |
| C5 | **B9 · varian protokol**: H02 biner, Meiligao OBD/DTC/RFID, Navigil MSG 13/15, sisa matriks TK103 | butuh **capture perangkat nyata** untuk verifikasi (tanpa itu = risiko silent corruption, preseden Codec 7 yang di-DROP) | sedang–besar |
| C6 | **B7 · presisi reverse-geocoding** (berhenti di level kota) | keterbatasan **sumber seed wilayah** (kecamatan/desa tanpa koordinat) — bukan bug kode | kecil–sedang |
| C7 | **B11 · audit asinkron** (kini sinkron; aman, tanpa drop) | optimalisasi latensi, bukan kebenaran | kecil |
| C8 | **Coverage non-inti ≥80%** (websocket 78,4 · media 67,1 · ingestion 62,5) | ambang internal; service inti **sudah** ≥80 | sedang |
| C9 | **Hardening produksi**: `govulncheck` (**✅ dikerjakan 2026-09-29 — lihat §E**), TLS/WSS, secrets manager, lint di CI | item infra/produksi, bukan cacat kode | sedang |
| C10 | **Frontend F1–F4** (Next.js, live map, playback, i18n) | fase terpisah; **belum dimulai** sesuai aturan "backend dulu" | sangat besar |

---

## D. KRITERIA "LAYAK PRODUKSI" — status objektif

| Kriteria | Target | Status | Bukti |
|---|---|---|---|
| Build & vet semua modul | bersih | ✅ | 9 modul |
| Test suite (hermetic) | 0 FAIL | ✅ | 14 paket `ok` |
| Integration test (DB nyata) | 0 FAIL | ✅ | `ADATRACK_IT=1` api-vehicle `ok` |
| E2E pipeline (device→NATS→Redis+PG) | lulus | ✅ | **5/5** |
| E2E WebSocket/RBAC | lulus | ✅ | **21/21** |
| E2E fuel (B5a) | lulus | ✅ | **11/11** |
| E2E media (B5b) | lulus | ✅ | **18/18** |
| E2E fleet (B7) | lulus | ✅ | baseline run |
| E2E commands/durable (B8) | lulus | ✅ | baseline run |
| **E2E enterprise (B12)** | lulus | ✅ **baru** | `scripts/e2e-enterprise.sh` |
| Migrasi otomatis pra-boot | ada | ✅ | `start-services.sh` + `migrate.sh` |
| Semua tenant ter-migrasi | 0 drift | ✅ | default/dev001/loadt2 = 027 |
| Ledger failure count | 0 | ✅ | master 24 / company 28 |
| `/healthz` 7 service | 200 | ✅ | 7/7 |
| Endpoint RBAC (deny by default) | 401 tanpa token | ✅ | 18/18 = 401 |
| JetStream retention | terbatas | ✅ | 48h / 16 GiB / 7 stream |
| Endurance 24 jam 0 loss | tercapai | ✅ (historis) | B4-VERIFICATION |
| Coverage service inti ≥80% | tercapai | ✅ | wp/wl/apiv |
| **Kebenaran PRD FR-4.1** | sesuai PRD | ⚠️ | A5 (butuh keputusan) |
| **TLS/WSS + secrets manager** | ada | ❌ | C9 (infra) |
| **Frontend** | ada | ❌ | C10 |

**Kesimpulan jujur:** backend **layak produksi untuk lingkup B2B yang sudah dibangun**
(kategori A & B tuntas). Yang menghalangi klaim "B0–B12 lengkap & siap jual penuh"
adalah **kategori C** (scope produk + frontend + TLS) — pekerjaan terjadwal, bukan
kecacatan tersembunyi.

---

## E. Keamanan — hasil `govulncheck` & perbaikannya (2026-09-29)

Dipasang `govulncheck` (Go 1.26.0) dan dijalankan atas **seluruh** modul.

### Temuan awal: 21 kerentanan yang benar-benar terpanggil
| # | Temuan | Modul | Diperbaiki di |
|---|---|---|---|
| 1 | **SQL injection via placeholder confusion with dollar-quoted literals** (GO-2026-5004) — **relevan tinggi**: proyek ini punya rewriter placeholder kustom (`internal/dialect/pgxdriver.go`) | `github.com/jackc/pgx/v5 v5.7.5` | **v5.9.2** |
| 2 | Infinite loop pada input invalid (GO-2026-5970) | `golang.org/x/text v0.35.0` | **v0.39.0** |
| 3–21 | 19 kerentanan **standard library** (`net/url`, `crypto/tls`, `net/http`, `crypto/x509`, `encoding/xml`, `encoding/asn1`, `net/textproto`, `net`, `os`) pada toolchain **go1.26.0** | stdlib | patch **go1.26.6** |

### Perbaikan yang dilakukan
1. **Semua 16 modul** dinaikkan: `pgx/v5 → v5.9.2`, `x/text → v0.39.0`, `go mod tidy`, build+vet+test hijau.
2. **Toolchain dipin & di-enforce**: `toolchain go1.26.6` di setiap `go.mod`; 8 Dockerfile build dengan **`golang:1.26.6-alpine`** (tag diverifikasi valid via `docker manifest inspect`) — sebelumnya floating `golang:1.25-alpine` yang tidak reprodusibel.
3. **Gate repeatable**: `scripts/vuln-scan.sh` + target **`make vuln`** (menjalankan govulncheck atas semua modul; gagal bila ada temuan yang terpanggil).

### Bukti
```
# sebelum:  "Your code is affected by 21 vulnerabilities from 2 modules and the Go standard library."  (exit 3)
# sesudah:  "No vulnerabilities found. Your code is affected by 0 vulnerabilities."                  (exit 0)
```
Test suite penuh **0 FAIL · 14 paket ok**; IT suite (`ADATRACK_IT=1`) **EXIT=0**; dan **seluruh 7 E2E dijalankan ulang** untuk membuktikan upgrade driver pgx tidak meregresi apa pun —
**105/105 check lulus**: `e2e` 5/5 · `e2e-ws` 21/21 · `e2e-fuel` 11/11 · `e2e-media` 18/18 · `e2e-fleet` 10/10 · `e2e-commands` 5/5 · `e2e-enterprise` 35/35.

### Sisa (jujur, infra — bukan kode)
- **TLS/WSS** untuk REST & WebSocket, serta **secrets manager** (kredensial saat ini lewat `.env`) — keputusan/penyediaan infra produksi.
- `golangci-lint`/`staticcheck` belum dipasang; `go vet` sudah bersih di semua modul.

