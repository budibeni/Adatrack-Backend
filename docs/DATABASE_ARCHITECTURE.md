# Database Architecture — General vs Transaction (Mengurangi Risiko "Stuck")

> Keputusan arsitektur & penjelasan untuk tim: **harus/tidaknya memisah database server antara data general (master) dan transaksi (telemetry)**.
> Konteks: Real-Time GPS Tracking & adatrack Management (PRD.md). Target: 5000+ device, 2000 msg/sec peak, latensi < 800 ms, uptime 99.9%.

---

## 1. Keputusan (TL;DR)

- **TIDAK wajib memisah server database** untuk menghindari *stuck* — asalkan desain schema/query benar.
- **Mulai dengan SATU instance PostgreSQL** (schema per-tenant, partitioned) + **batch insert** + **connection pooling** + **index** yang tepat.
- Pemisahan server (general vs telemetry) **baru dipertimbangkan** di skala yang sangat besar; bukan langkah awal.
- Jika beban reading tinggi, pendekatan yang paling efektif = **read replica** (pisah beban read vs write), bukan memecah logical database.

---

## 2. Apa Penyebab "Stuck" di Database?

Stuck/lambat di database hampir selalu karena **lock contention + query lambat**, bukan tipe data-nya:

- Query panjang / full table scan yang **menahan lock lebih lama**.
- Transaksi terlalu besar/lama (mis. insert tidak di-batch).
- Tabel terus membesar tanpa partitioning → range lock melebar.
- Koneksi habis (pooling buruk / query menggantung).

> Penting: **memisah server TIDAK otomatis menyembuhkan penyebab ini** — query yang lambat tetap bisa membuat server terpisah tampak "stuck".

---

## 3. Workload di Project Ini: "General" vs "Transaksi/Telemetry"

| Jenis | Contoh tabel | Pola workload |
|---|---|---|
| **Data master/reference ("general")** | `users`, `vehicles`, `geofences`, `alerts`, `user_vehicles` | Read/write rendah, update kadang → **OLTP reference** |
| **Data telemetry/transaksi** | `telemetry_logs` (high-volume), alert events | Insert massal, write-heavy → **streaming** |

Keduanya memang berbeda pola, tetapi **dapat ditangani dalam satu instance** dengan mitigasi di §4.

---

## 4. Mitigasi "Stuck" dalam SATU Instance

1. **Partitioning** `telemetry_logs` per bulan → batch insert singkat, lock kecil (PRD FR-3.x).
2. **Batch insert** (500 record / 20 detik) → transaksi pendek, tidak memegang lock lama.
3. **Index yang tepat** (`idx_vehicle_time`, `idx_imei_time`, spatial `idx_location`) → hindari full scan & lock memanjang.
4. **Connection pooling** 20–50 → hindari koneksi habis / antrean menunggu.
5. **PostgreSQL tuning**: `shared_buffers`, `wal_buffers`, `max_wal_size`, transaction isolation disesuaikan untuk workload write-heavy.

Dengan ini satu PostgreSQL **mampu memenuhi target 2000 msg/sec peak** tanpa pemisahan server.

---

## 5. Yang Lebih Bernilai daripada Memecah Server: Pemisahan Read vs Write

Stuck sering muncul karena kompetisi antara **reader berat (history/analytics/dashboard)** vs **writer (insert telemetry)**. Solusi paling efektif = **read replica**:

```
App/Worker (insert)      ──►  Primary (write) PostgreSQL
Dashboard/History/Analytics  ──►  Read replica
```

- Query history 30 hari, geofence query, laporan → arahkan ke **replica**.
- Insert dari `worker-persistence` → tetap ke **primary**.
- Keunggulan: memisah beban TANPA memecah integritas data; masih bisa JOIN antar tabel (satu logical schema).

---

## 6. Kapan Baru Perlu Server Terpisah?

Hanya bila skala melampaui kemampuan satu instance, mis.:
- Sustained lebih dari beberapa ribu msg/sec, atau
- Retention sangat panjang + ukuran DB raksasa, atau
- Butuh isolation/billing terpisah antar tenant.

Urutan bertahap yang disarankan: **partitioning → vertical scale → read replica → (baru) split cluster**.

### Risiko pemisahan server (jangan diadopsi dini)
- **Hilangnya FK/JOIN lintas DB** → harus di-join di application layer.
- **Tanpa distributed transaction** → risiko konsistensi data.
- Operasional lebih kompleks: backup, monitoring, biaya.
- Harus sinkron/double-write terkelola.

---

## 7. Rekomendasi & Panduan Tim

1. **Mulai dengan 1 instance PostgreSQL** + partitioning + batch insert + pooling (sesuai PRD & `backend/docker-compose.yml`).
2. **Jangan mengadopsi "pemisahan general vs telemetry"** di tahap awal — biaya integritas lintas-DB & operasional tinggi, dan tidak menyelesaikan akar penyebab *stuck*.
3. Gunakan **read replica** (di fase B4) bila beban reading tinggi.
4. Bila muncul **lock contention / slow query** → itu sinyal optimasi query/index, bukan langsung memecah server (lihat `docs/INCIDENT_RUNBOOK.md` I5/I6/I7).

---

## 8. Referensi Terkait

- Schema database: `PRD.md §6`
- Performance/query SLA: `PRD.md §10`
- Monitoring & incident (lock contention, slow query, RDS): `docs/INCIDENT_RUNBOOK.md`
- Phase yang menyentuh: B0 (schema/partition), B1 (batch insert), B4 (replica, index, load test)