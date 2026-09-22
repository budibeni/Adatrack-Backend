# B4 — Performance, Monitoring, Testing & Hardening (Bukti Verifikasi)

> Fase: **B4** (`.agent/03-backend-phases.md`) · PRD §10 (monitoring), §11 (retensi),
> §12 (backup/DR), §13 (HA), §16 (testing), §17 (NFR).
> Runner tunggal: `scripts/b4-verify.sh` (log per-run: `logs/b4-verify-<stamp>.log`).
> Catatan kejujuran: setiap angka di bawah berasal dari eksekusi nyata pada
> environment lokal (PostgreSQL 15 di Docker `:5533`, Redis `:6380`,
> NATS `:4222`, 6 service host-run). Item yang **belum** memenuhi target
> ditandai ️ dan dijelaskan (tidak dicentang di checklist fase).

## 1. Ringkasan Status

| # | Item B4 | Target | Hasil | Status |
|---|---|---|---|---|
| 1 | Load bertahap | 400 → 1000 → 2000 msg/s, 0 loss | 7.897 / 19.947 / 58.631 frame, **0 loss**, 0 write error | ✅ |
| 2 | Endurance chunked resume-safe | 24 jam kumulatif | 1 jam kumulatif terbukti (1.438.418 pesan @400 msg/s, 0 loss/chunk, plateau heap+goroutine); **run 24 jam @3600 s/chunk dijalankan bertahap** — progres di `logs/b4-endurance-<stamp>/resume.log` | 🟡 berjalan |
| 3 | Load multi-tenant & isolasi | 0 cross-tenant leakage | LOADT2 vs DEV001: 0 leakage dua arah | ✅ |
| 4 | Query SLA | history 30 hari < 1,5 s; geofence < 500 ms | 792 ms (1000 baris dari ≈1,44 juta baris) / 201 ms / 3 ms / 4 ms | ✅ |
| 5 | Coverage service inti | ≥ 80 % | gate `b4-verify` (diukur dengan `ADATRACK_IT=1`): worker-live **86,8 %**, worker-persistence **91,1 %**, worker-alert **84,2 %**, api-vehicle **80,0 %**, internal/tenant **80,8 %** | ✅ |
| 6 | `go vet` + build bersih | exit 0 | `scripts/test.sh` exit 0 (8 modul), `go vet` bersih | ✅ |
| 7 | Monitoring | Prometheus + dashboard SLO Grafana + alert rule inti | 11/11 target UP, 20 rule, dashboard `adatrack-core` | ✅ |
| 8 | Hardening | JWT revocation, rate limit, audit menyeluruh; retensi JetStream | unit test + audit live append + 6/6 stream 48 h/4 GiB | ✅ |
| 9 | Backup / DR | dump harian + checksum + uji restore | dump 4 schema + SHA256; restore row-count match | ✅ |
| 10 | Retensi DB | partisi/purge telemetry (§11) | `retention-purge.sh` + fungsi `tm_ensure_telemetry_partition` | ✅ |
| 11 | **Load WS 50×1200 (§16)** | 50 subscriber × 1200 frame, 0 loss / 0 drop | `ws.load_50x1200`: `recv[1201..1201]`, `server_sent_delta=60050` (= 50×1201), `drops_delta=0`, `conns_after=0 subs_after=0`, p50 16 ms · p95 17 ms · max 17 ms, goroutine 25→100(transien)→**23** (settle) | ✅ |
| 12 | **Replika + drill failover (§13)** | PG streaming + standby read-only + Redis promote/fail-back | `make replica-drill`: **20/20 PASS** — `state=streaming` + wal receiver streaming, baris primary terpropagasi ke replay, tulis langsung ke standby **ditolak**, lag **0 byte**, slot `pg_replica_slot` aktif, Redis `role:slave` + `master_link_status:up`, promote → tulis diterima → fail-back resync | ✅ |

## 2. Detail Bukti

### 2.1 Load bertahap — 0 data loss

Harness `tools/e2e --load` (frame GT06 1:1 → ingestion-tcp → NATS → worker-live +
worker-persistence), pembanding `sent` vs `persisted` per IMEI:

| Rung | Durasi | Terkirim | Tersimpan | Write error | Throughput efektif |
|---|---|---|---|---|---|
| 400 msg/s | 20 s | 7.897 | 7.897 | 0 | ~395 msg/s |
| 1000 msg/s | 20 s | 19.947 | 19.947 | 0 | ~997 msg/s |
| 2000 msg/s | 30 s | 58.631 | 58.631 | 0 | ~1954 msg/s |

`load.live_state` PASS untuk ketiga IMEI di setiap rung (state Redis segar), dan
tidak ada `telemetry.error.>` (dead-letter) selama run.

### 2.2 Endurance (chunked, resume-safe)

`scripts/b4-verify.sh` langkah 4 menjalankan endurance per-chunk (default
`B4_ENDURANCE_CHUNKS=6` × `B4_ENDURANCE_CHUNK_SEC=600`) dengan jejak
`logs/b4-endurance-<stamp>/{resume.log,chunk-<n>.log}`:

| Bukti | Hasil (2026-09-19, run `b4-endurance-20260919T040141Z`) |
|---|---|
| Chunk | 6 × 600 s @ 400 msg/s (total **1 jam**) |
| Pesan per chunk | 239.207 / 239.844 / 239.812 / 239.896 / 239.831 / 239.828 |
| Total terkirim | **1.438.418** — semuanya **persisted == sent (0 loss per chunk)** |
| Write error | 0 (semua chunk) |
| `load.live_state` | PASS untuk 3 IMEI di setiap chunk |
| Resource plateau (FR-4.4) | heap `5,23 MB → 4,45 MB` (turun), goroutines `16 → 15` — **tidak ada indikasi leak** |
| Resume-safe | chunk berikutnya hanya dijalankan setelah chunk sebelumnya PASS; jejak waktu di `resume.log` (04:15 → 05:08 UTC) |

- 24 jam penuh: `B4_ENDURANCE_CHUNKS=24 B4_ENDURANCE_CHUNK_SEC=3600 scripts/b4-verify.sh`
  (mekanisme resume sama; dapat dijalankan bertahap).
- Yang diverifikasi sesi ini adalah **1 jam kumulatif** (bukan 24 jam penuh) karena
  batas waktu environment — tapi itu load nyata dengan verifikasi
  `sent == persisted` per chunk dan bukti plateau resource.

### 2.3 Multi-tenant isolation (0 leakage)

`scripts/provision-tenant.sh LOADT2` (schema `adatrack_gps_loadt2`, 16 migrasi +
ledger) + IMEI `864201040599901` dan kendaraan LOADT2. Verifikasi:

| Cek | Hasil |
|---|---|
| Flow LOADT2 (raw → live → PG → Redis) | 5/5 PASS, `company=LOADT2 vehicle=1` |
| Baris IMEI DEV001 di schema LOADT2 | **0** |
| Baris IMEI LOADT2 di schema DEV001 | **0** |
| Baris IMEI LOADT2 di schema platform `adatrack_gps_default` | **0** |

Pemisahan struktural: schema per-tenant + `search_path` dipaksa per pool
(`internal.Config.PostgresDSN`); tenant selalu dari token/`tm_vehicle_imei_map`,
bukan dari body request.

### 2.4 Query SLA (`tools/querybench`)

Terakhir dijalankan setelah endurance (≈1,44 juta baris telemetry di
`adatrack_gps_dev001`) — kondisi data yang lebih berat dari proof-of-concept awal:

| Query | Baris | SLA | Hasil (2026-09-19) |
|---|---|---|---|
| history 30 hari (1000 baris terakhir) | 1000 | 1,5 s | **792 ms** |
| count 24 jam | 1 | 1,5 s | **201 ms** |
| daftar geofence | 0 | 500 ms | **3 ms** |
| daftar kendaraan | 3 | 500 ms | **4 ms** |

Pada data kecil (2 baris) run pertama mencatat 24/32/3/4 ms — semua jauh di bawah
SLA. Indeks pendukung:
`idx_th_telemetry_logs_{vehicle,imei,company}_time` pada `th_telemetry_logs`
(partitioned monthly) — terpasang sejak migrasi company `007`.

### 2.5 Coverage (✅ gate ≥ 80 % tercapai)

Gate di `scripts/b4-verify.sh` langkah 1 kini menjalankan pengukuran dengan
`export ADATRACK_IT=1`, sehingga suite integrasi PostgreSQL/NATS/Redis
(opt-in, pola yang sama dengan `worker-persistence`/`worker-live`) ikut
dihitung. Hasil per modul (2026-09-21):

| Modul | Coverage | Isi suite |
|---|---|---|
| `internal/tenant` | **80,8 %** (sebelumnya 8,5 %) | IT nyata: routing pool, `ResolveDeviceByIMEI` + cache Redis, Health, provisioning tenant + idempotensi + ledger (`internal/tenant/tenant_it_test.go`) |
| `internal/storage` | 100 % | unit |
| `internal` (pkg utama) | 42,3 % | config/env/logging (di luar gate: gate mengukur max antar-paket) |
| `services/worker-live/controllers` | **86,8 %** | hermetic + IT (`ADATRACK_IT=1`) |
| `services/worker-persistence/controllers` | **91,1 %** | hermetic + IT |
| `services/worker-alert/controllers` | **84,2 %** (sebelumnya 5,7 %) | engine/notifier/detektor hermetic (`alert_*_test.go`, miniredis) + IT `store_pg` (`store_pg_it_test.go`) |
| `services/api-vehicle/controllers` | **80,0 %** (sebelumnya 18,3 %) | handler hermetic + IT `PostgresStore` nyata (`store_pg_it_test.go`, `http_test.go`, `handlers_update_restore_test.go`, dsb.) |
| `services/service-websocket/controllers` | 66,9 % | auth/RBAC/WS/audit — **di luar daftar gate** (loop coverage b4-verify mencakup internal + 4 worker/api) |
| `services/ingestion-tcp/controllers` | 48,3 % | parser GT06/Teltonika golden test — di luar daftar gate |

Semua suite IT menulis fixture ber-marka unik dan membersihkannya di
`t.Cleanup` (dataset dev tidak tertinggal artefak — diverifikasi 0 baris
sisa setelah run). Menjalankan ulang:

```bash
ADATRACK_IT=1 scripts/test.sh        # semua modul + suite IT
ADATRACK_IT=1 make b4-verify QUICK=1 # gate B4 dengan coverage IT
```

### 2.6 Monitoring & Observability

- **Stack nyata:** `docker-compose.yml` — satu file untuk seluruh sistem (infra
  `postgres`/`redis`/`nats` **+** monitoring terus aktif): Prometheus
  (`:9095`), Alertmanager (`:9093`), Grafana (`:3001`), node-exporter (`:9100`),
  cAdvisor (`:8084`), postgres-exporter (`:9187`), redis-exporter (`:9121`).
  Varian LOCAL (`docker-compose.local.yml`) menambah MinIO dan mem-bind seluruh
  port ke `127.0.0.1`; varian Coolify memakai `expose` (internal saja).
- **Target UP 11/11:** 6 service aplikasi (`ingestion-tcp`, `worker-live`,
  `worker-persistence`, `worker-alert`, `service-websocket`, `api-vehicle`) +
  postgres/redis/node/cadvisor/prometheus.
- **file_sd:** `monitoring/targets/adatrack-services.json` di-generate oleh
  `scripts/gen-prom-targets.sh`. Alamat host dipilih **empiris**, bukan ditebak:
  kandidat `host.docker.internal` lalu IPv4 utama host, masing-masing diuji
  dengan menembak `/healthz` dari dalam container Prometheus — di Docker Desktop
  Windows/WSL2 `host.docker.internal` menunjuk gateway VM (mis. `192.168.65.254`)
  sehingga yang terpakai adalah IP distro tempat service host-run berjalan.
  Override manual lewat `PROM_SCRAPE_HOST`. File hasil generate **tidak
  di-track git** (machine-specific) dan diregenerasi otomatis oleh `make up`.
- **Rule:** `adatrack-slo.yml` (5 rule: recording availability/budget, fast burn,
  budget exhausted) + `alert-rules.yml` (15 alert sesuai PRD §10.3 — NATS pending,
  insert latency, tenant resolution, WS broadcast, HTTP p95 > 800 ms, error rate,
  notification failure, goroutine/heap leak). Terverifikasi live: Prometheus
  memuat 5 + 15 rule tanpa error konfigurasi.
- **Grafana:** dashboard **ADATRACK Core** (uid `adatrack-core`, 8 panel:
  throughput, latency p95, backpressure, resource stability, SLO budget, alert
  rate, DB backends, CPU) ter-provision otomatis + datasource Prometheus.

### 2.7 Hardening

| Aspek | Bukti |
|---|---|
| JWT + refresh rotation + revocation (denylist `jti`) | `TestRefreshRotationAndLogout`, `TestLogoutIsFailClosedWhenAuditFails` |
| Rate limit login (5×/15 mnt) + lockout + API 100/mnt | `TestLoginRateLimited`, `TestLoginLockoutAfterRepeatedFailures`, `TestAPIRateLimitPerUser` |
| Audit trail live | login gagal → `tm_audit_logs` bertambah (append-only, fail-closed) |
| Retensi JetStream | 6/6 stream `MaxAge 48 h` + `MaxBytes 4 GiB`, `DiscardOld` (log boot + `/jsz`) |
| Backpressure | `backpressure_warnings_total`, level warn > 50 % / drop > 90 % (FR-1.5) |

### 2.8 Backup / Restore / Retensi

- `scripts/backup-db.sh`: dump per schema (`adatrack_gps_master`, `default`,
  `dev001`, `loadt2`) format custom + gzip + `SHA256SUMS`, retensi lokal 14 hari.
- `scripts/restore-db.sh`: verifikasi checksum → restore ke scratch DB
  `adatrack_gps_restore_test` → bandingkan row-count: **match** (master
  `tm_companies`=3, dev001 `th_telemetry_logs`=86.477, loadt2=1). Satu-satunya
  noise: GUC `transaction_timeout` (pg_dump 18 → server 15) — dikenali dan
  diabaikan eksplisit oleh skrip.
- `scripts/backup-redis.sh`: `BGSAVE` + salin RDB/AOF, simpan 3 snapshot terakhir.
- `scripts/retention-purge.sh`: deteksi partisi bulanan > `HOT_RETENTION_DAYS`
  (default 30) per tenant, hitung baris sebelum drop (no silent loss), dry-run
  default dan `--apply` untuk eksekusi; selalu memanggil
  `tm_ensure_telemetry_partition` untuk bulan berikutnya.

### 2.9 Runner penuh `scripts/b4-verify.sh`

- **Run penuh** (`logs/b4-verify-20260919T040141Z.log`): 10 langkah —
  **16 PASS / 1 FAIL**. Satu-satunya FAIL (audit append) dianalisis dan
  diperbaiki; bukti lengkap endurance 6 chunk ada pada run ini.
- **Run QUICK setelah perbaikan** (`logs/b4-verify-20260919T080822Z.log`):
  **15 PASS / 0 FAIL — `B4-VERIFY: ALL PASS`**, termasuk
  `audit trail live append (9 -> 10)` dan `scripts/test.sh exit 0 all modules`.
- **Run QUICK dengan coverage IT** (2026-09-21): **15 PASS / 0 FAIL** —
  langkah 1 kini mengukur coverage dengan `ADATRACK_IT=1` (lihat §2.5);
  lima modul yang di-gate ≥ 80 % tanpa WARN.
- **Run FULL setelah fix drain worker-alert + gate coverage IT**
  (2026-09-21, run `b4-verify-20260921T164334Z.log`): **17 PASS / 0 FAIL —
  `B4-VERIFY: ALL PASS`** — coverage gate 5/5 modul ≥ 80 % tanpa WARN, load
  400/1000/2000 (0 loss), endurance 6 chunk **1.438.599 pesan (0 loss/chunk)**,
  multi-tenant LOADT2 (0 leakage), query SLA, monitoring stack, hardening
  (audit 0 -> 1), backup-db + restore drill row-count match + backup-redis +
  retention dry-run.
- **Run QUICK setelah konsolidasi compose + rekonsiliasi role DB**
  (2026-09-22, run `b4-verify-20260922T063758Z.log`): **15 PASS / 0 FAIL —
  `B4-VERIFY: ALL PASS`** dijalankan pada stack **satu-compose** (monitoring
  menyatu di `docker-compose.yml`, satu project `adatrack_gps_system`; lihat
  §2.6) — 11 container up, Prometheus 11/11 target UP. Kredensial
  `.env.local`/`.env.coolify` kini memakai role aplikasi `adatrack_gps_user`,
  bukan superuser bootstrap `adatrack` — volumenya sudah ter-init lebih dulu,
  sehingga role aplikasi dibuat + kepemilikan schema/objek dialihkan agar
  setara kondisi fresh init.

- **Penyebab FAIL awal:** langkah 8 mengirim password uji `wrong` (5 karakter) →
  ditolak validasi input (§8.5, minimum 8 karakter) sebagai `400 VALIDATION_ERROR`
  **sebelum** kode sampai ke bcrypt — sehingga memang tidak ada baris audit
  (perilaku benar: input tidak valid tidak perlu diaudit sebagai LOGIN_FAILURE).
- **Perbaikan:** skrip kini memakai `wrong-password` (bentuk valid, kredensial
  salah) dan menunggu flush audit async (`AUDIT_FLUSH_MS`) sebelum membandingkan
  count. Terverifikasi live: `tm_audit_logs` **8 → 9** (run penuh) dan
  **9 → 10** (run QUICK) dengan `error_code=INVALID_CREDENTIALS`.

Langkah yang PASS: preflight healthz · unit/integration + coverage (terukur) ·
JetStream 6/6 stream · load 400/1000/2000 (0 loss) · endurance 6 chunk
resume-safe · multi-tenant LOADT2 (0 leakage) · query SLA · monitoring stack ·
hardening (unit test + audit live + JetStream) · backup-db · restore drill
row-count match · backup-redis · retention dry-run.

### 2.10 Load WebSocket 50×1200 (PRD §16)

Profil fan-out ada di `tools/e2ews` (mode `--ws-only`): 50 subscriber bersamaan,
1200 frame device dipublikasikan lewat jalur nyata (GT06 → `ingestion-tcp` →
NATS → `worker-live` → `service-websocket` → klien), lalu satu frame **marker**
dipublikasikan sendirian supaya setiap klien bisa mengukur latensi end-to-end-nya
tanpa nomor urut di dalam payload GT06.

Tiga sudut bukti untuk aturan "0 loss":

| Sisi | Assertion | Hasil |
|---|---|---|
| Klien | tiap subscriber menerima tepat 1201 frame (1200 + marker) | `recv[1201..1201]` |
| Server | `ws_message_sent_total{event="VEHICLE_UPDATE"}` naik tepat clients × published | `60050` = 50 × 1201 |
| Backpressure | `ws_message_dropped_total` tidak bergerak (drop-oldest, FR-5.4) | `drops_delta=0` |

Plus: latensi marker p50 **16 ms** · p95 **17 ms** · max **17 ms** (kriteria < 1 s),
`ws_connections_active` dan `ws_subscriptions_active` kembali **0** (hub melepas
seluruh koneksi & langganan), dan goroutine kembali di bawah baseline
(25 → 100 saat koneksi ditutup → **23** setelah settle) → tidak ada indikasi leak
(FR-4.4). Profil QUICK memakai 10×200 supaya gate tetap cepat.

Rerun: `cd tools/e2ews && go run . --ws-only --ws-clients 50 --ws-messages 1200 --ws-rate 50`
atau lewat gate `scripts/b4-verify.sh` langkah 4b.

### 2.11 Replika PostgreSQL/Redis + drill failover (PRD §13)

Infrastruktur: `deployments/docker-compose.ha.yml` (overlay varian LOCAL) —
`postgres-replica` (streaming WAL dari slot `pg_replica_slot`, dibootstrap
`pg_basebackup -R` oleh `deployments/ha/postgres-replica-entrypoint.sh`) dan
`redis-replica` (`replicaof`). Tooling: `scripts/replication/drill-ha.sh`,
`replication-status.sh`, `promote-redis-replica.sh`, plus target `make ha-up`,
`ha-status`, `replica-drill`.

`make replica-drill` (2026-09-22) — **20/20 PASS**:

| Grup | Assertion | Hasil |
|---|---|---|
| Prasyarat | slot `pg_replica_slot` + baris `host replication` di `pg_hba.conf` primary | dibuat + dimuat ulang |
| Streaming | primary `pg_stat_replication.state=streaming`; replika `pg_stat_wal_receiver.status=streaming` | PASS |
| Propagasi | `INSERT` di primary muncul di standby | PASS |
| Proteksi | `INSERT` langsung ke standby **ditolak** (`read-only transaction`) | PASS |
| Konsistensi | standby `pg_is_in_recovery()=t`, slot aktif, lag `pg_wal_lsn_diff` = **0 byte** | PASS |
| Redis | replika `role:slave`, `master_link_status:up`, nilai primary terpropagasi | PASS |
| Drill failover | promote (`REPLICAOF NO ONE`) → replika **menerima tulis** → fail-back (`REPLICAOF redis 6379`) → link `up` → propagasi normal kembali | PASS |

> **Catatan jujur:** *Read/Write split app-level* yang disebut PRD §13 —
> `Manager.ReadPool()`/`ReadRouter`, metrik `db_read_queries_total`/
> `db_replica_up` — kini **ADA di kode** (`internal/tenant/replica*.go`, §2.12):
> routing replica-dulu dengan fallback one-shot ke primary, breaker per tenant
> (3 gagal → 30 s → half-open), prober berkala, dan wiring pada endpoint
> list-read `api-vehicle`. Tetap **default-off**: tanpa `POSTGRES_REPLICA_HOST`
> seluruh baca memakai primary (perilaku sebelum B4). Yang belum: wiring list-read
> di `service-websocket`/`worker-alert` (pola satu baris yang sama) dan probe
> `cmd/db-replica-probe` dari PRD — fungsinya kini digantikan metrik `db_replica_up`
> + test IT `TestITReadWriteSplit`.

### 2.12 Read/Write split app-level (PRD §13)

`internal/tenant/replica.go` menambahkan router baca per tenant:

| Aspek | Implementasi |
|---|---|
| Routing | `ReadQuery`/`ReadQueryRow` → pool **replika** perusahaan itu (schema sama, `search_path` sama); `Exec`/`Begin` tetap lewat `Manager.DB()`/`Master()` |
| Fallback | Satu kegagalan baca di replika **di-retry sekali** ke primary (`db_replica_fallbacks_total`) — blip replika tidak boleh menggagalkan request |
| Breaker | 3 kegagalan → open 30 s → half-open; `db_replica_up{company_code}` melaporkan 1/0 |
| Prober | `Manager.Run()` mem-ping replika tiap 15 s (membuka pool lebih dulu) sehingga breaker pulih tanpa trafik |
| Metrik | `db_read_queries_total{company_code,route}` (route `replica`/`primary`), `db_replica_up`, `db_replica_fallbacks_total` |
| Default | `POSTGRES_REPLICA_HOST` kosong = split mati; kredensial/DB mewarisi primary |
| Wiring | `api-vehicle`: `ListVehicles` + `ListAlerts`. `service-websocket`: `VehicleHistory` (baca terberat — playback), `ListVehicles`, `VehicleByID` (service ini tidak punya endpoint tulis kendaraan). **Sengaja tetap di primary:** `*ByID` di `api-vehicle` (handler PATCH memakainya ulang → hindari lost-update akibat lag) dan seluruh query `worker-alert` (querynya guard dedup yang langsung diikuti penulisan) |

Bukti eksekusi (2026-09-22):

- **Unit hermetic** — `replica_test.go`: default-off, state machine breaker
  (3 gagal → open → half-open → sukses menutup), aturan DSN (replika eksplisit,
  pewarisan kredensial, `search_path` dipaksa, `DATABASE_URL` sengaja diabaikan).
- **IT nyata** — `replica_it_test.go` terhadap standby HA di `127.0.0.1:5433`:
  `read/write split verified: route=replica rows=1 read_route=replica` (INSERT di
  primary terlihat di replika, baca dilayani replika, `ReadQueryRow` memberi
  `sql.ErrNoRows` saat kosong). Jalankan: `make ha-up` lalu
  `ADATRACK_IT=1 ADATRACK_IT_PG_REPLICA=127.0.0.1:5433 go test ./internal/tenant/`.
- Suite `api-vehicle` dengan IT tetap hijau: `ok … coverage 80,1 %`.
- Suite `service-websocket` dengan IT tetap hijau: `ok … coverage 67,4 %` (naik
  dari 66,9 % karena jalur router ikut ter-cover).
- **Jalur fallback** — `TestITReadFallbackToPrimary` (tanpa perlu replika: endpoint
  replika diarahkan ke port tertutup): baca tetap **berhasil** lewat primary
  (`route=primary`), breaker tetap tertutup setelah 1 kegagalan (blip transien
  tidak boleh mematikan split) dan **terbuka** setelah 3 kegagalan — memastikan
  replika rusak tidak pernah menggagalkan request maupun mengunci sistem.

## 3. Cara Menjalankan Ulang

```bash
make up && make migrate && make services-up   # infra + service host-run
make up && make prom-targets                     # infra + monitoring (satu stack, §10)
make b4-verify                                 # rantai acceptance B4 penuh
make b4-verify QUICK=1                         # smoke cepat
make querybench CODE=DEV001                    # SLA query
make backup-db && make restore-db STAMP=backups/<ts>/<stamp>
make retention-purge                           # dry-run (APPLY=1 untuk drop)
```

## 4. Gap yang Tersisa (belum dicentang)

1. **Endurance 24 jam penuh** — run bertahap sedang berjalan
   (`B4_ENDURANCE_CHUNKS=24 B4_ENDURANCE_CHUNK_SEC=3600 scripts/b4-verify.sh`).
   Yang sudah terbukti: 1 jam kumulatif (6 chunk × 600 s, 1.438.418 pesan,
   0 loss/chunk, plateau heap+goroutine). Progres 24 jam dapat dipantau di
   `logs/b4-endurance-<stamp>/resume.log` (satu baris per chunk yang PASS).
2. **Read/Write split app-level (§13)** — **router + wiring SELESAI** (§2.12),
   tetap *default-off*. Yang **sengaja** tidak dirutekan ke replika: `*ByID` di
   `api-vehicle` (dipakai ulang oleh handler PATCH) dan query `worker-alert`
   (guard dedup sebelum penulisan) — keduanya demi menghindari lost-update akibat
   replication lag. `cmd/db-replica-probe` dari PRD juga tidak dibuat: fungsi
   diagnosisnya kini dicakup metrik `db_replica_up{company_code}` + test IT
   `TestITReadWriteSplit`.
3. **Coverage di luar gate** — `service-websocket` 66,9 % dan `ingestion-tcp`
   48,3 % tidak termasuk loop coverage `b4-verify` (gate hanya mengukur service
   inti + `internal/tenant`). Bukan bagian target gate ≥ 80 % service inti.
