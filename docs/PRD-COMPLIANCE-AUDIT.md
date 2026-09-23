# Audit Kepatuhan PRD — Backend (read-only, 2026-09-23)

Dijalankan **selama run acceptance B4 berjalan** (chunk endurance aktif), jadi audit
ini sengaja hanya memakai operasi baca: grep, baca file, dan query SQL read-only.
Tidak ada test, tidak ada restart service, tidak ada perubahan skema.

Tujuannya: menemukan celah "PRD mengatakan X, kode melakukan Y" seperti kasus
`idx_timestamp` (FR-3.5) yang membuat SLA 30 hari gagal pada 7,77 juta baris.

## 1. Ringkasan

| # | Item PRD | Status | Bukti singkat | Tindakan |
|---|---|---|---|---|
| 1 | FR-3.5 `idx_timestamp` | ✅ diperbaiki | migrasi `017`; `history.30d` 5.952 ms → **34 ms** | selesai (B4-VERIFICATION §2.4) |
| 2 | FR-3.5 `idx_location` + kolom `location` | ❌ tidak ada | `information_schema` → kolom `location` **false**; skema pakai `latitude`/`longitude` numerik | putuskan: PostGIS + indeks spasial, atau catat deviasi (§2.1) |
| 3 | FR-4.1 delivery durable (ack/NACK, redelivery) | ❌ berbeda | **tidak ada konsumsi JetStream**: satu-satunya jalur `internal.NATSClient.Subscribe` → `conn.QueueSubscribe` (core NATS, at-most-once) | keputusan desain (§2.2) |
| 4 | FR-4.1 `MaxPending: 10.000 msgs` | ⚠️ tidak dipetakan | `natsclient` hanya memakai `MaxBytes`/`MaxAge`; `MaxPending` di config adalah batch buffer FR-4.4 | dokumentasikan atau map ke `MaxMsgs` (§2.3) |
| 5 | FR-4.1 queue groups (persistence/live/websocket/alert/media) | ✅ | `Subscribe(..., "persistence"/"live"/"alert"/"websocket", ...)` | — |
| 6 | FR-4.1 `MaxInflight per subscriber: 100` | ⚠️ dibuat tapi tak terpakai | `AddConsumer{MaxAckPending: 100}` ada, tapi konsumer durable tidak dikonsumsi | ikut §2.2 |
| 7 | FR-1.5 warn >50 % / drop >90 % | ✅ terverifikasi | `BackpressureLevel`: `>=90 → drop`, `>=MaxPendingPercent(50) → warn`; terbukti saat insiden | — |
| 8 | Port service (§7/§14) | ⚠️ PRD tidak konsisten | PRD menulis `api-vehicle 18081` (§14) vs `8081` (§7); implementasi **8081** | selaraskan PRD (bukan kode) |
| 9 | Coverage service inti ≥80 % | ✅ | B4-VERIFICATION §2.5: internal 91,9 · wp 91,1 · wl 86,8 · wa 84,2 · apiv 80,1 | — |
| 10 | Coverage service non-inti | ⚠️ | service-websocket **78,4** · service-media **67,1** · ingestion-tcp **62,5** | dikerjakan setelah run (§3) |

## 2. Temuan terpenting

### 2.1 FR-3.5 — spatial index belum ada (deviasi tercatat)

PRD:
```sql
CREATE SPATIAL INDEX idx_location ON th_telemetry_logs (location);
```
Realita: kolom `location` **tidak ada** di `th_telemetry_logs` (terverifikasi lewat
`information_schema`); posisi disimpan sebagai `latitude`/`longitude` (numeric).
Dampak praktis sekarang **rendah**: query geofence/radius tidak mengandalkan indeks
spasial dan sudah jauh di bawah SLA (`geofence.list` 5–7 ms; history 30 hari 34 ms
setelah migrasi `017`). Risiko muncul bila nanti ada query "device dalam radius X"
pada tabel telemetry berskala puluhan juta baris — saat itu PostGIS + indeks GiST
diperlukan.

### 2.2 FR-4.1 — PRD menjanjikan delivery durable; implementasi at-most-once

Fakta (grep seluruh `services/` + `internal/`; **tidak ada** hasil konsumsi JetStream):

```
internal/natsclient.go:217   sub, err := c.conn.QueueSubscribe(subject, queueGroup, ...)
service-websocket            Subscribe(subject("live", ">"),        "websocket",   ...)
worker-persistence           Subscribe(subject("raw", ">"),         "persistence", ...)
worker-live                  Subscribe(subject("raw", ">"),         "live",        ...)
worker-alert                 Subscribe(subject("raw", ">"),         "alert",       ...)
```

Tidak ada `js.Subscribe` / `PullSubscribe` / `Consume` di kode mana pun. Akibatnya:

- Pesan yang dipublikasikan **saat worker mati/restart hilang** (tanpa redelivery).
  Terlihat saat operasional: `start-services.sh up` di tengah alur membuat telemetri
  in-flight tidak diproses.
- Buffer retensi JetStream (48 h / MaxBytes) **tidak pernah di-replay** — fungsinya
  hanya sinyal backpressure, bukan pemulihan.
- Konsumer durable (`DeliverNewPolicy`, `AckWait 30 s`, `MaxAckPending 100`) dibuat
  setiap boot tetapi **tidak dipakai** → menyesatkan pembaca kode, dan `make js-status`
  menampilkan `pending` besar yang bukan backlog nyata (pernah salah dibaca sebagai
  "konsumer macet" — lihat B4-VERIFICATION §2.14).

Dua opsi yang sah, tapi harus **eksplisit** (saat ini PRD dan kode berbeda diam-diam):

- **(a) Tetap core NATS** — sederhana dan latensi rendah; PRD §4.1 direvisi menjadi
  delivery at-most-once, buffer JetStream hanya untuk observabilitas/backpressure.
- **(b) JetStream pull-consume + ack** — sesuai PRD §4.1 (`persist until ACK/NACK`,
  `MaxInflight 100`), tahan restart, buffer bisa di-replay. Biaya: redesign konsumer
  di keempat worker + pengukuran ulang throughput/SLA.

Rekomendasi engineering: **(b) untuk `worker-persistence`** (data harga mati: baris
telemetry/fuel tidak boleh hilang) dan **(a) untuk `worker-live`/`websocket`**
(state live boleh hilang, lalu diisi ulang oleh frame berikutnya), lalu PRD diperbarui.

### 2.3 FR-4.1 — `MaxPending` 10.000 pesan tidak dipetakan

PRD menyebut `MaxPending: 10.000 msgs (~40 s buffer @250 msg/s)`. Di kode, batas
stream hanya `MaxBytes` + `MaxAge` (`ensureStreams`), sedangkan `MaxPending` di
`internal/config.go` adalah *batch buffer* worker (FR-4.4) — bukan pengganti.
Konsekuensi: "40 s buffer" versi PRD tidak dijamin; yang berlaku adalah ~48 jam /
16 GiB (rumus kapasitas di B4-VERIFICATION §2.14).

### 2.4 Konsistensi deployment setelah perubahan kapasitas

- `deployments/docker-compose.coolify.yml` memakai **file `nats.conf` yang sama**
  (`../deployments/nats/nats.conf:ro`), jadi `max_file_store: 100GB` berlaku di
  LOCAL maupun Coolify.
- `.env.coolify` masih `JETSTREAM_MAX_BYTES=4294967296` (4 GiB) → 6 stream × 4 GiB
  = 24 GiB ≤ 100 GB ✔ (tanpa degradasi cap oleh `ensureStreams`).
- `max_file_store` adalah **batas**, bukan prealokasi: host Coolify hanya perlu ruang
  untuk pemakaian *aktual* (retensi 48 jam pada laju nominal ±2–3 GB).
- Bila kelak `JETSTREAM_MAX_BYTES` dinaikkan (mis. untuk endurance/stress di server),
  aturan wajibnya: `max_file_store ≥ 6 × JETSTREAM_MAX_BYTES`, kalau tidak cap per
  stream akan diturunkan bertahap (fail-safe, cap jadi tidak seragam).

## 3. Checklist pasca-run (setelah endurance selesai)

1. Baca `B4 SUMMARY` di `logs/b4-verify-<stamp>.log`; pastikan `endurance 24/24`
   (laporan kini jujur: run yang berhenti lebih awal tercatat `X/Y`, bukan `24`).
2. `make cover` (tabel lengkap 8 modul) → perbarui angka final di §2.5.
3. Perbaiki label statis step 2 di `scripts/b4-verify.sh` ("48h/4GiB per stream" →
   dibaca dari config). **Jangan** diedit saat run berjalan (offset bash).
4. Jalankan test yang tertunda + tutup celah berikut:
   - `service-media`: `retention.startRetention`, `service.Start/Stop`, `kv` (Redis
     wrapper), `auth` denylist helper, `errors.Error/errInternal`,
     `metrics.currentClaims/requireWrite`.
   - `service-websocket`: jalur audit dead-letter, `bridge.RegisterBridgeMetrics`.
   - `ingestion-tcp`: `main`, `publishTelemetry` (butuh NATS), `handleGT06` login.
5. Housekeeping data: endurance menambah ±35 juta baris telemetry; jalankan
   `make retention-purge` (dry-run) lalu `APPLY=1` bila ingin membebaskan ruang.
6. Verifikasi pasca-run: `make js-status` (usage kembali kecil), `make js-guard`,
   `make e2e` + `make e2e-ws` (sanity), lalu commit rekap akhir.

## 4. Aturan selama run (pelajaran dari insiden sebelumnya)

- **Jangan** jalankan `make cover`, `scripts/test.sh`, `make services-up`,
  `docker compose restart`, atau `make e2e*` saat chunk berjalan: beban paralel
  membuat persistence tertinggal (chunk 5 kehilangan 10 frame) dan restart service
  memutus delivery core NATS (§2.2).
- Aman selama run: `make js-status`, `make js-guard`, `tail`/`cat` log, query SQL
  read-only, dan pekerjaan dokumentasi seperti dokumen ini.

