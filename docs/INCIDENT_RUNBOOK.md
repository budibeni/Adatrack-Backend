# INCIDENT RUNBOOK — Penanggulangan Otomatis & Manual

> Dokumen operasional untuk **menangani insiden** pada platform Real-Time GPS Tracking & adatrack Management.
> Berlaku untuk semua layer: service backend (Go), infra (CPU/Memory), messaging (NATS/Redis), dan database (PostgreSQL/RDS).
> Referensi metrik & threshold: `PRD.md §8`, Phase B4 di `.agent/03-backend-phases.md`.

---

## 1. Tujuan & Prinsip

1. **Tujuan:** setiap insiden punya jalur penanganan — **otomatis dulu**, lalu **manual (runbook)** bila otomatis tak cukup.
2. **Prinsip utama (dari PRD):**
   - **Zero data loss** → jangan matikan service pipeline utama tanpa pengaman.
   - Latensi end-to-end < 800 ms; uptime ≥ 99.9%; throughput 2000 msg/sec peak.
   - **Auto-healing > manual restart.** Manual restart adalah *last resort* untuk kondisi *stuck*, bukan untuk beban tinggi.
   - Untuk **beban/frekuensi tinggi** gunakan **scaling**, bukan restart.
3. **Jangan pernah *silent* kehilangan data** — setiap drop di-log & di-alert.

---

## 2. Severity & Prioritas Penanganan

| Severity | Contoh | Target tindakan |
|---|---|---|
| **SEV-1 (CRITICAL)** | Persistence gagal memanjang, koneksi DB habis, NATS down, data loss terdeteksi | Tanggulangi segera + eskalasi on-call/lead |
| **SEV-2 (HIGH)** | CPU > 85%, memory > 80%, slow query melonjak, WS broadcast > 1s | Tanggulangi cepat, investigasi penyebab |
| **SEV-3 (WARNING)** | CPU > 70%, error rate > 5%, queue mendekati max | Pantau, gejalakan auto-mitigasi |

Urutan penanganan: **identifikasi (metrik) → auto-mitigasi → manual runbook → verifikasi pemulihan → post-mortem**.

---

## 3. Mitigasi Otomatis (Pertahanan Berlapis)

Harus aktif sebelum insiden terjadi. Diterapkan sejak phase B0–B4:

1. **Health check & auto-restart:** orchestrator (Docker restart policy / K8s liveness probe / systemd `Restart=`) me-restart service yang `/healthz` gagal.
2. **Auto-scaling:** scale out saat CPU > 70% (5 mnt) atau queue menumpuk; scale in saat CPU < 30% (GAP #7).
3. **Backpressure & shedding:** ingestion-tcp menahan/drop data non-kritis saat NATS pending > 50–90%; WebSocket drop pesan saat queue > 1000 (FR-4.2).
4. **Retry + exponential backoff:** operasi NATS/PostgreSQL/Redis gagal = retry 1s/5s/10s (max 3) (FR-4.3). Persistence publish ke `telemetry.error.<IMEI>` bila habis.
5. **Connection pooling + TTL:** PostgreSQL pool 20–50, Redis pool 10–30, Redis key state TTL 5 mnt (self-expire).
6. **Partitioning PostgreSQL:** pisah data per bulan → paksa data lama keluar, kurangi lock contention, mudahkan purge.
7. **Alerting rules** (Prometheus → Alertmanager): trigger di threshold (lihat §4 & tiap skenario).

---

## 4. Threshold Monitoring (Acuan Dashboard)

| Metrik | WARNING | CRITICAL | Aksi |
|---|---|---|---|
| CPU host/container | > 70% (5 mnt) | > 85% | Scaling / cek query berat |
| Memory host | > 80% | > 90% | Cek leak / OOM risk |
| `go_goroutines` | trend naik merata | naik tajam terus | Cek goroutine leak |
| NATS pending | > 50% max (5000) | > 90% (9000) | Shedding / scale persistence |
| PostgreSQL connections | > 80% max | > 90% | Cek pooling / tutup idle |
| `slow_queries` | bertambah | melonjak | Optimasi query / indeks |
| WS broadcast | — | > 1 s | Cek network / scaling WS |
| Error rate | — | > 5% | Investigasi root cause |

---

## 5. Runbook per Insiden

### I1. CPU Tinggi (service / host / container)

**Deteksi:** metric `node_cpu`, `container_cpu_usage_seconds_total`, alert CPU > 70% (W) / > 85% (C).

- **Otomatis:** auto-scaling (tambah instance worker/websocket); load balancer menyeimbangkan; NATS queue group mendistribusi pesan antar instance.
- **Manual:**
  1. Identifikasi instance/container mana yang tinggi (Grafana "adatrack Core" → grup Host/Container).
  2. Cek apakah karena **query DB berat** (lihat I7) atau **loop/log storm** (`go_goroutines` naik).
  3. Naikkan kapasitas instance (vertical scale) bila pertumbuhan organik.
  4. Optimasi bottleneck: indeks, batch size, batasi log level (`LOG_LEVEL=info` min).

### I2. Memory Tinggi / Mengancam OOM

**Deteksi:** `container_memory_working_set_bytes` > batas, `process_resident_memory_bytes` naik, host memory > 80%, indikasi eviction/OOMKilled.

- **Otomatis:** cAdvisor + node_exporter memicu alert; orchestrator me-restart container OOMKilled; Redis eviction policy (maxmemory) mencegah Redis memenuhi host.
- **Manual:**
  1. Cek container mana (Grafana Memory), lihat log `OOMKilled` / eviction.
  2. Cek **Go runtime**: `go_gc_duration_seconds`, heap growth → kemungkinan leak.
  3. Ukur `go_goroutines` per jam; bila naik merata → **goroutine leak** (lihat I3).
  4. Perbesar limit/swappiness bila organik; tambah instance bila skala horisontal.
- **Catatan:** memory leak di service pipeline bisa memicu data loss bila container di-recycle di tengah batch → pastikan batch redelivery NATS berjalan.

### I3. Goroutine Leak / Service Stuck (Deadlock)

**Deteksi:** `go_goroutines` naik terus, service tidak respons (healthz lambat), request timeout, WS broadcast macet.

- **Otomatis:** liveness probe (~healthz) gagal N× → auto-restart container.
- **Manual:**
  1. Ambil **goroutine dump** (`GET /debug/pprof/goroutine?debug=2` pada service) → cari stack yang tergantung.
  2. Cek kebocoran memori / channel tak tertutup / lock tak dilepas.
  3. Bila stuck & pprof tidak cukup → **restart service tertentu** (bukan ingestion-tcp) sesuai konvensi terbatas & tercatat.
  4. Perbaiki root cause (mis. tambah timeout pada query, konteks cancel per koneksi).
- **Verifikasi:** `go_goroutines` stabil/fluktuatif wajar; healthz kembali < 1s; latensi normal.

### I4. Throughput / Frekuensi Tinggi (Queue Backlog)

**Deteksi:** NATS pending mendekati 50–90% (5000–9000), batch insert lambat, WS broadcast > 1s.

- **Otomatis:** backpressure/shedding (ingestion menahan/drop non-kritis saat pending > 50/90%, FR-4.2); **auto-scaling** worker persistence/live; queue group distribusi.
- **Manual:**
  1. **JANGAN restart dulu** (memperburuk backlog). Identifikasi penyebab: surge device, worker lambat, DB bottleneck.
  2. Tambah instance worker persistence/live (scale out) untuk kejar backlog.
  3. Cek DB (I6/I7) bila persistence jadi penghambat.
  4. Sempitkan jendela batch (`BATCH_TIMEOUT_SEC`) atau naikkan `BATCH_SIZE` agar throughput lebih besar per siklus.
- **Verifikasi:** pending menurun & stabil < 50%; latency kembali < 800ms; zero drop yang tak terjawab di error queue.

### I5. RDS / PostgreSQL: Koneksi Habis

**Deteksi:** `DatabaseConnections` / `pg_stat_database_numbackends` mendekati `max_connections` (> 80% C).

- **Otomatis:** pool service (min 20 / max 50) membatasi; stmt timeout 30s membatalkan query menggantung; alert CRITICAL.
- **Manual:**
  1. Identifikasi koneksi bocor: `SELECT * FROM pg_stat_activity WHERE state = 'active'` — cari query `idle` panjang / menggantung.
  2. Kecilkan idle pool service; pastikan konteks query punya timeout.
  3. Tutup koneksi idle dari sisi server bila perlu (sementara).
  4. Naikkan `max_connections` (hati-hati memory) bila SLA butuh selisih lebih besar.
- **Verifikasi:** `pg_stat_database_numbackends` stabil jauh di bawah max; tidak ada koneksi terlepas tak terkendali.

### I6. RDS / PostgreSQL: Slow Query & Lock Contention

**Deteksi:** `slow_queries` melonjak, query historis > 1.5s, geofence query > 500ms, locked/blocked transaction naik.

- **Otomatis:** `long_query_time` menghasilkan penanda slow query (slow log); alert slow_queries; partitioning mengurangi lock contention.
- **Manual:**
  1. Baca **slow query log** → temukan query N+1 / tanpa indeks.
  2. Pastikan indeks ada: `idx_vehicle_time`, `idx_imei_time`, spatial `idx_location` (FR-3.5).
  3. Cek **locked tx**: cari transaksi lama yang menahan lock; batalkan bila diperlukan.
  4. Sempitkan batch insert agar tiap lock singkat; lakukan pada jam sepi bila memungkinkan.
- **Verifikasi:** `slow_queries` menurun; query history < 1.5s terpenuhi kembali.

### I7. RDS / PostgreSQL: I/O Tinggi, Latency Tinggi, Disk Penuh

**Deteksi:** `WriteLatency`/`ReadLatency` meninggi, `DiskQueueDepth` tinggi, `FreeableMemory` rendah, penyimpanan hampir penuh, `ReplicaLag` (bila replica).

- **Otomatis:** alerting RDS; partitioning + purge otomatis archive (GAP #4) melepas disk; provisioning IOPS mencukupi.
- **Manual:**
  1. Cek IOPS vs provisioning; naikkan class instance / IOPS bila bottleneck I/O.
  2. Cek **retensi data**: drop/archive partisi lama (pastikan data sudah ter-archive sebelum drop).
  3. Tambah storage / perbesar storage type bila mendekati penuh.
  4. Set non-sync (async) replica bila `ReplicaLag` tinggi (sementara), lalu cek catch-up.

### I8. Redis Bermasalah (Redis Down / Memory Overflow)

**Deteksi:** `redis_errors_total` naik, `vehicle:state` kosong, connection timeout, TTL state menghilang.

- **Otomatis:** Redis TTL 5 mnt (state self-expire) → saat Redis down, service pakai fallback / dedupe; `maxmemory` + eviction policy mencegah host penuh; restart otomatis container Redis.
- **Manual:**
  1. Cek status Redis (`INFO`, `ping`) & memory (`maxmemory-*`, evicted keys).
  2. TTL state 5 mnt → setelah recovery, worker-live **re-sync state** dari telemetry NATS (tidak perlu restore penuh).
  3. Perbesar maxmemory / tambah memory host bila eviction berlebihan.
  4. Verifikasi `vehicle:state:<IMEI>` kembali terisi.
- **Catatan:** sesuai GAP #5, Redis failure = state basi ≤ 5 mnt — dianggap *acceptable* (vehicle offline), data riwayat tetap aman di PostgreSQL.

### I9. NATS Down / Message Loss

**Deteksi:** publish/subscribe gagal, `nats_publish_errors_total` naik, pending hilang, producer/consumer tidak saling terhubung.

- **Otomatis:** NATS dengan JetStream (`-js`) mempertahankan pesan sampai ACK; service retry + exponential backoff; restart otomatis container NATS.
- **Manual:**
  1. Periksa status NATS (port 4222, `/varz` pada 8222) & stream/pending.
  2. Pastikan JetStream aktif; cek stream & durable consumer pada worker persistence.
  3. Saat NATS pulih, service otomatis re-subscribe & memproses backlog.
  4. Data loss ≤ 10k msg yang belum di-NACK dianggap *acceptable* (GAP #5) — catat & audit.
- **Verifikasi:** pending ter-clear, tidak ada worker yang tertinggal, throughput kembali normal.

### I10. Persistence Error Berulang (Retry Habis)

**Deteksi:** `batch_insert_errors_total` naik, `retry_attempts_total` timbul, pesan masuk `telemetry.error.<IMEI>`.

- **Otomatis:** retry 1s/5s/10s (max 3); setelah habis, pesan di-NACK dan di-publish ke `telemetry.error.<IMEI>` + di-log + alert.
- **Manual:**
  1. Periksa `telemetry.error.*` & log error penyebab insert gagal (schema? duplicate? DB down?).
  2. Cek DB sehat (I5/I6/I7) sebelum meresume.
  3. Re-process error queue bila root cause sudah diperbaiki.
  4. Audit jumlah drop; laporkan ke lead bila berpotensi data loss.
- **Verifikasi:** `batch_insert_errors_total` berhenti naik; error queue ter-clear.

### I11. WebSocket Overload / Broadcast Lambat

**Deteksi:** `ws_connections_active` mendekati limit (5000), `ws_broadcast_duration_ms` > 1s, `ws_message_queue_size` > 1000 per koneksi.

- **Otomatis:** limit max conn (FR-5.4); send buffer 256KB + queue 1000 → drop pesan lama + log warning; ping 30s deteksi koneksi mati.
- **Manual:**
  1. Cek jumlah koneksi aktif vs user; tambah instance service-websocket (scale out).
  2. Pastikan fan-out di-filter RBAC (hanya vehicle milik user) agar bukan broadcast global.
  3. Cek QoS jaringan / peningkatan limit bila legitimate user bertambah.
  4. Kurangi pesan non-kritis bila masih lambat di sisi client.

### I12. GPS Device Mass Disconnect

**Deteksi:** `tcp_connections_active` anjlok, `vehicle:state` menua (offline massal), `tcp_connections_total` tidak bertambah.

- **Otomatis:** timeout idle 90s / offline 3 mnt menandai device OFFLINE; worker-live menghitung status; alert OFFLINE massal (jika lonjakan).
- **Manual:**
  1. Periksa penyebab: ISP/protocol change, firmware, timeout, jaringan device.
  2. Pastikan `ingestion-tcp` sehat & listener aktif (port 9000) sebelum menyalahkan device.
  3. Koordinasi dengan pihak device/ISP bila bermasalah di sisi mereka.
  4. Device akan otomatis reconnect (TCP keep-alive); pantau reconnect rate.
- **Verifikasi:** koneksi pulih, `vehicle:state` ter-update, tidak ada backlog baru.

---

## 6. Alur Eskalasi

- **SEV-1 (CRITICAL):** hubungi on-call/lead segera; semua stakeholder diberi tahu; gunakan prosedur DR bila DB down (GAP #5).
- **SEV-2 (HIGH):** selesaikan dalam 4 jam kerja; laporkan ke lead bila berlarut.
- **SEV-3 (WARNING):** pantau & tandai di sistem; selesaikan dalam 1 hari kerja.

**Yang wajib dicatat pada setiap insiden:** kronologi, metrik terlibat, keputusan auto vs manual, dampak (terutama potensi data loss), dan hasil verifikasi pemulihan.

---

## 7. Recovery & Verifikasi (Checklist Akhir)

Setelah insiden teratasi, lakukan:
1. **Cek data integrity:** pastikan tidak ada baris `telemetry_logs` hilang tanpa tercatat (bandingkan count vs hash di error queue).
2. **Cek SLA:** latensi end-to-end < 800ms, `batch_insert_duration_ms` normal, query history < 1.5s.
3. **Cek infra:** CPU < 70%, memory normal, DB connections < 80%, pending NATS < 50%.
4. **SLO uptime:** hitung dampak; bila < 99.9% dalam rentang, catat post-mortem.
5. **Dokumentasikan** di runbook/langkah baru bila ditemukan kondisi baru.

> **Filosofi:** **auto-mitigasi dulu, manual sebagai cadangan.** Setiap solusi manual yang berulang harus di evaluasi untuk diotomatisasi di fase B4 (monitoring, autoscaling, auto-healing, alerting).