# High Availability (HA) & Disaster Recovery — Strategi "Murah & Mudah"

> Keputusan arsitektur untuk tim: **cara menangani "server down" agar secara otomatis/manual ditanggulangi server lain**.
> Prinsip yang dipilih: **TIDAK mahal, TIDAK rumit, mudah dipahami orang awam.**
> Konteks: Real-Time GPS Tracking & adatrack Management (PRD.md). Target 5000+ device, 2000 msg/sec peak, uptime 99.9%.
>
> **STATUS 2026-09-13 (PRD konsolidasi v1.6.0):** engine persisten canonical = **PostgreSQL**
> (PRD §7.1, schema-per-tenant). Seluruh prosedur di dokumen ini memakai jalur
> PostgreSQL: primary + streaming replica (WAL), dump harian + uji restore,
> drill failover Redis.

> ⚠️ **UPDATE 2026-08-25 — Perubahan fungsi REPLICA:** replica database
> (PostgreSQL) kini diposisikan sebagai **READ replica** (melayani query
> baca; primary tetap satu-satunya jalur TULIS) dan **BUKAN** untuk backup
> maupun failover-promote. Skrip promote **database** telah dihapus;
> panduan promotion di dokumen ini menjadi referensi manual-only (bukan alur
> operasional saat ini). Proteksi data tetap via dump harian + uji restore.
>
> **PENGECUALIAN REDIS:** sesuai §3 di bawah (*state live TTL 5 mnt,
> kehilangan ≤5 mnt diterima*), replika Redis **tetap memakai jalur
> failover-cepat** — `promote-redis-replica.sh` dipertahankan dan prosedurnya
> wajib dilatih via `backend/scripts/replication/drill-redis-failover.sh`
> (sudah LIVE ✓ 2026-08-25).
>
> ### Playbook: jika syarat penjagaan read-replica terlanggar (2026-08-25)
> | Syarat dilanggar | Gejala/sinyal | Tindakan |
> |---|---|---|
> | #1 Recovery tak pernah dilatih / primary mati sungguhan | Alert ServiceDown + primary unreachable | Ikuti §5 di bawah secara MANUAL (PG: `SELECT pg_promote()`), arahkan service ke replika, lalu rebuild primary lama (`pg_basebackup`). Setelah pulih: jadwalkan drill 6-bulanan |
> | #2 RPO dump harian (~24 jam) dinilai terlalu besar | Review manajemen / insiden data | Naikkan ke PITR: PostgreSQL `archive_command` + WAL-G ke storage terpisah. WAJIB uji restore PITR sekali sebelum dipercaya |
> | #3 Slot WAL/disk membengkak | Alert `PgSlotWALRetention*`, `PgSlotNotReserved`, `HostDiskSpace*` | Diagnosis `replication-status.sh`. Replica mati lama → hidupkan; tak bisa → drop slot lalu resync (`pg_basebackup -R` ulang dgn volume replica kosong). Disk penuh → bersihkan dump/WAL lama atau tambah kapasitas. JANGAN drop slot bila replica masih dibutuhkan segar |
> | #4 Aplikasi salah rute baca/tulis | Read-after-write basi; error read-only saat tulis nyasar | Tulis SELALU ke primary DSN. Baca ke replika hanya utk query toleran-lag (history/report) + feature flag; fallback ke primary bila lag > ambang. Guardrail: integration test assert endpoint tulis memakai DSN primary |
>
> **Pemicu review keputusan:** RPO ketat (<24 jam), produksi multi-server aktif,
> beban baca melebihi kapasitas satu replika.

---

## 1. Keputusan (TL;DR)

- Gunakan **2 server: PRIMARY (melayani) + STANDBY (siap ganti)** — pendekatan *active-passive* yang sederhana.
- **Semua service** ter-install di kedua server; normalnya hanya PRIMARY yang melayani trafik.
- **PostgreSQL**: PRIMARY = primary DB, STANDBY = **replica** (data selalu tersalin, bisa dipromosikan bila PRIMARY mati).
- **Redis**: PRIMARY = master, STANDBY = **replica** (state live; failover cepat).
- **NATS**: aktif di server yang melayani; pakai JetStream untuk mengurangi pesan hilang.
- **Failover**: dibantu otomatis + runbook manual 10 langkah yang mudah diikuti.
- **Backup PostgreSQL** harian + uji restore → proteksi data (bukan hanya proteksi server).

> 💡 Kenapa TIDAK pakai hal yang rumit/mahal (multi-region, Kubernetes, InnoDB Cluster, dsb.)?
> Karena tidak butuh itu di tahap ini, dan malah lebih sulit dirawat. Struktur primary+standby ini menutupi
> sebagian besar skenario "server mati" dengan biaya & usaha minimal.

---

## 2. Arsitektur

```
                        ┌──────────────── Server A (PRIMARY) ────────────────┐
Device / User ──► LB/DNS ──►  ingestion-tcp · service-websocket · api-vehicle  │
                                 worker-live · worker-persistence · worker-alert
                                  PostgreSQL (primary) · Redis (master) · NATS
                        └───────────────┬──────────────────────────────┘
                                         │ replikasi PostgreSQL + Redis (terus-menerus)
                        ┌───────────────▼──────────────────────────────┐
                        └────────  Server B (STANDBY) ─────────────────┘
                                  · PostgreSQL (replica, siap di-promote)
                                 · Redis (replica)
                                 · service-service (idle, siap dinyalakan)
```

Alur normal: A melayani. B menyalin data (replica). Bila A mati → **failover** → B dipromosikan menjadi primary (lihat §5).

---

## 3. Penanganan Per Komponen (paling penting untuk dipahami)

| Komponen | Sifat | Di A (PRIMARY) | Di B (STANDBY) | Bila A mati |
|---|---|---|---|---|
| `ingestion-tcp`, `api-vehicle`, `service-websocket`, semua worker | stateless | Aktif | Ter-install, idle | B diaktifkan (jalankan service di B) |
| **PostgreSQL** | state (data) | Primary | **Replica** (sync terus) | B di-promote jadi primary (-± cepat, data utuh) |
| **Redis** | state live (TTL 5 mnt) | Master | Replica | B di-promote (state ≤5 mnt) |
| **NATS** | pesan sementara | Aktif | Siap aktif | B aktifkan; JetStream kurangi kehilangan pesan |

### Penjelasan singkat per lapisan
1. **Service stateless (copyA semua):** tanpa state → tinggal menyalakan di B saat A mati. Ini yang paling mudah & paling sering jadi sumber "server down".
2. **PostgreSQL (inti, paling penting):** gunakan **replication** dari A → B. Data terus tersalin. Saat failover, B di-promote menjadi primary baru. **Berikan cara otomatis/script** + runbook manual agar tidak kehilangan data.
3. **Redis:** state hanya *live* (lokasi terakhir, TTL 5 menit) — memakai **master-replica** + Sentinel bila mau otomatis. Kehilangan ≤5 mnt masih diterima (lihat runbook I8). Riwayat tetap aman di PostgreSQL.
4. **NATS:** aktif di node yang melayani. Dengan JetStream + retry (sudah direncanakan), pesan yang belum sempat diproses saat transisi tetap bisa diminimalkan kerugiannya.

---

## 4. Backup Data (bagian dari "server backup")

- **PostgreSQL**: backup harian (pg_dump/pg_basebackup) + WAL berkelanjutan. Bila pakai AWS → **RDS automated backup + point-in-time recovery (PITR)**.
- **Partisi & retention**: pindahkan/archive partisi lama agar backup tidak membengkak (GAP #4).
- **Uji restore berkala** — backup yang tidak pernah diuji tidak berguna.
- **Redis/NATS** tidak wajib di-backup (data live/sementara), cukup replikasi & mekanisme recovery.

---

## 5. Proses Failover (dibawa & mudah diikuti)

**Deteksi "server A mati":** ping A + cek `/healthz` pada `ingestion-tcp`/`api-vehicle`/`worker`. Gagal terus-menerus (> 3 cek berturut) = A dianggap turun.

**Runbook 10 langkah (failover A → B):**
1. Tetapkan A tidak responsif (ping + healthz gagal berulang).
2. Beritahukan tim (buka runbook insiden / `docs/INCIDENT_RUNBOOK.md`).
3. Pada **B**: **manual-only** (lihat banner 2026-08-25 di atas — replika DB = READ replica, skrip promote database telah dihapus): bila darurat, promote replica → primary (`SELECT pg_promote()`). Alur operasional saat ini TIDAK memakai promote DB.
4. Pada **B**: promote Redis replica → master (atau aktifkan Sentinel jika dipakai).
5. Pada **B**: jalankan service stateless (ingestion-tcp, api-vehicle, service-websocket, worker-*).
6. Mulai NATS di B (JetStream) & pastikan worker bisa connect.
7. Pindahkan trafik: rubah DNS/VIP/port → mengarah ke **B**.
8. Verifikasi: `/healthz` B OK, data terbaca, device mulai connect ke B, dashboard jalan.
9. Pantau beberapa menit (pending menurun, tidak ada error).
10. Dokumentasikan insiden; rencanakan perbaikan/mengembalikan A setelah diperbaiki (lakukan **fail-back** via runbook yang sama, alur terbalik).

**Fail-back (B → A):** setelah A diperbaiki, jadikan A replica kembali, tunggu sync, baru promote A sebagai primary & pindahkan trafik.

---

## 6. Kapan "Level" Ini Tidak Cukup (Catatan Kejujuran)

- Level di dokumen ini **melindungi dari "1 server mati"** (host down). 
- **TIDAK** menjanjikan toleransi terhadap bencana luas (mis. satu zona/daerah mati total) — untuk itu baru perlukan DR cross-region (lebih mahal; di luar tier ini).
- Database dalam satu "Penyewa" (data center) tetap punya risiko jika seluruh penyewa tersebut mati. Bila target uptime 99.9% + SLA perusahaan menghendaki, upgrade ke RDS Multi-AZ / DR region dapat dibahas di B4.

---

## 6b. Pemakaian Replika oleh Aplikasi — Read/Write Split (implementasi B4)

Sejak fase B4 (2026-08-25), replika database **tidak lagi idle**: aplikasi
memakainya untuk **skala baca**, sesuai keputusan READ REPLICA di atas.

**Aturan routing (dipakai SEMUA service via `internal/tenant`):**

| Operasi | Jalur | Keterangan |
|---|---|---|
| `SELECT` handler GET API (list/detail/history/alerts/geofences/routes) | **REPLICA** | pool read terpisah per tenant |
| `SELECT` worker-alert (vehicle by IMEI, config speed/geofence/route, penerima notifikasi) | **REPLICA** | query terpanas (tiap telemetry 5 dtk/device) |
| Loader vehicle-registry service-websocket (cache-miss bridge NATS→WS fanout) | **REPLICA** | `companyReadByCode`; cache in-mem TTL 5 mnt |
| `INSERT/UPDATE/DELETE` (alerts, notifications, CRUD vehicles/routes/configs) | **PRIMARY** | selalu — tanpa pengecualian |
| Guard dedup (`HasOpenAlert`) + auth master (`users`, `vehicle_imei_map`) | **PRIMARY** | konsistensi-kritis; lag replika bisa menimbulkan duplikat/gagal login |
| Redis live-state | **PRIMARY** | replika Redis = jalur failover cepat (latih via drill), bukan untuk baca latency-sensitive |

**Mekanisme ketahanan:** breaker per-tenant (3 kegagalan beruntun → buka 30 dtk,
half-open sesudahnya) + prober `Ping` berkala (`DB_REPLICA_PROBE_SECONDS`)
+ **fallback satu-kali ke primary** saat query replica gagal — tiap fallback
ter-log warn dan ter-metric; tidak ada silent drop.

**Env (`backend/.env`):** `DB_REPLICA_ENABLED` (default false),
`DB_REPLICA_HOST`, `POSTGRES_REPLICA_PORT`,
`DB_REPLICA_POOL_MIN/MAX`, `DB_REPLICA_PROBE_SECONDS`. `DATABASE_URL`
hanya berlaku untuk PRIMARY (replika selalu eksplisit).

**Metrik baru (Prometheus):** `db_read_queries_total{company_code,route}`
(route = `replica|primary|primary_fallback`) dan `db_replica_up{company_code}`.

**Verifikasi live 2026-08-25 (PostgreSQL):** replika :5533 menjawab
`pg_is_in_recovery()=t, transaction_read_only=on`; SELECT schema tenant
`adatrack_gps_dev001` sukses via replika; INSERT ditolak engine
("cannot execute INSERT in a read-only transaction"). Unit test router
(routing/fallback/breaker/half-open/Exec-primary) hijau; build + vet + test
bersih untuk internal, service-websocket, api-vehicle, worker-alert.
**Probe live app-level** (`backend/cmd/db-replica-probe`, jalankan dari
direktori tool): TenantManager nyata → ReadPool & ReadRouter mendarat di
REPLIKA (`pg_is_in_recovery=true`), `router.Primary()` di PRIMARY, metrik
`db_read_queries_total{route=replica}=2 primary=0` — ✅ PROBE LOLOS.

**Tuning produksi 2026-08-25:** interval prober produksi `DB_REPLICA_PROBE_SECONDS=5`.

**Batasan jujur:** replika bersifat asynchronous — pembacaan bisa telat
(milidetik di LAN). Endpoint yang menulis lalu langsung membaca kembali dalam
satu request sengaja memakai jalur primary sehingga *read-your-writes* aman;
pembacaan lintas-request setelah tulis dapat melihat data sedetik lebih tua.
Replika TIDAK digunakan untuk backup/failover database (lihat §4/§5).

---

## 7. Ringkasan & Panduan Praktis (untuk orang awam)

1. Buat **2 server** = PRIMARY + STANDBY, pakai **docker-compose** yang sama di keduanya.
2. Tambah **restart policy `unless-stopped`** pada semua service → kalau service crash, restart otomatis (lapis ke-1 proteksi).
3. Aktifkan **PostgreSQL replication** (A→B) — ini sumber utama "server backup" untuk data.
4. Aktifkan **Redis master-replica** (A→B).
5. Simpan **runbook failover** di atas, latih minimal sekali (uji failover).
6. Jalankan **backup PostgreSQL** harian + uji restore.
7. Masuk fase **B4 (High Availability & Monitoring)** di `.agent/` — termasuk metrik kesehatan server A/B.

---

## 8. Referensi Terkait
- Incident handling umum (CPU/RDS/service stuck/dll): `docs/INCIDENT_RUNBOOK.md`
- Keputusan database (single instance vs split): `docs/DATABASE_ARCHITECTURE.md`
- Backup/DR & retention: GAP #4 & #5 pada `PRD.md`