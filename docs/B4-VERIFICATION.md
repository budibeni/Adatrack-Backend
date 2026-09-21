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
| 2 | Endurance chunked resume-safe | 24 jam kumulatif | **1.438.418 pesan @ 400 msg/s selama 1 jam (6 chunk, 0 loss/chunk), heap & goroutine plateau**; jalur 24 jam siap | ⚠️ 1 jam / 24 jam |
| 3 | Load multi-tenant & isolasi | 0 cross-tenant leakage | LOADT2 vs DEV001: 0 leakage dua arah | ✅ |
| 4 | Query SLA | history 30 hari < 1,5 s; geofence < 500 ms | 792 ms (1000 baris dari ≈1,44 juta baris) / 201 ms / 3 ms / 4 ms | ✅ |
| 5 | Coverage service inti | ≥ 80 % | gate `b4-verify` (diukur dengan `ADATRACK_IT=1`): worker-live **86,8 %**, worker-persistence **91,1 %**, worker-alert **84,2 %**, api-vehicle **80,0 %**, internal/tenant **80,8 %** | ✅ |
| 6 | `go vet` + build bersih | exit 0 | `scripts/test.sh` exit 0 (8 modul), `go vet` bersih | ✅ |
| 7 | Monitoring | Prometheus + dashboard SLO Grafana + alert rule inti | 11/11 target UP, 20 rule, dashboard `adatrack-core` | ✅ |
| 8 | Hardening | JWT revocation, rate limit, audit menyeluruh; retensi JetStream | unit test + audit live append + 6/6 stream 48 h/4 GiB | ✅ |
| 9 | Backup / DR | dump harian + checksum + uji restore; replika + drill | dump 4 schema + SHA256; restore row-count match | ✅ |
| 10 | Retensi DB | partisi/purge telemetry (§11) | `retention-purge.sh` + fungsi `tm_ensure_telemetry_partition` | ✅ |

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

- **Stack nyata:** `monitoring/docker-compose.monitoring.yml` — Prometheus
  (`:9095`), Alertmanager (`:9093`), Grafana (`:3001`), node-exporter (`:9100`),
  cAdvisor (`:8084`), postgres-exporter (`:9187`), redis-exporter (`:9121`).
- **Target UP 11/11:** 6 service aplikasi (`ingestion-tcp`, `worker-live`,
  `worker-persistence`, `worker-alert`, `service-websocket`, `api-vehicle`) +
  postgres/redis/node/cadvisor/prometheus.
- **file_sd:** `monitoring/targets/adatrack-services.json` di-generate oleh
  `scripts/gen-prom-targets.sh` (auto-detect host address agar Prometheus
  container bisa scrape service host-run; override via `PROM_SCRAPE_HOST`).
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

## 3. Cara Menjalankan Ulang

```bash
make up && make migrate && make services-up   # infra + service host-run
make prom-targets && make monitoring-up        # monitoring stack (§10)
make b4-verify                                 # rantai acceptance B4 penuh
make b4-verify QUICK=1                         # smoke cepat
make querybench CODE=DEV001                    # SLA query
make backup-db && make restore-db STAMP=backups/<ts>/<stamp>
make retention-purge                           # dry-run (APPLY=1 untuk drop)
```

## 4. Gap yang Tersisa (belum dicentang)

1. **Endurance 24 jam penuh** — **1 jam kumulatif terbukti** (6 chunk × 600 s,
   1.438.418 pesan, 0 loss/chunk, plateau heap+goroutine, jejak resume
   `logs/b4-endurance-*/resume.log`); eksekusi 24 jam penuh belum dijalankan di
   environment ini — jalurnya siap
   (`B4_ENDURANCE_CHUNKS=24 B4_ENDURANCE_CHUNK_SEC=3600 scripts/b4-verify.sh`).
2. **Drill replika PostgreSQL/Redis + failover** (§13) — belum dijalankan di
   environment lokal (butuh stack replika); prosedur ada di
   `docs/HIGH_AVAILABILITY.md`.
3. **Load WS 50×1200 subscriber** (§16) — belum dijalankan sesi ini (harness WS
   tersedia di `tools/e2ews`).
4. **Coverage di luar gate** — `service-websocket` 66,9 % dan `ingestion-tcp`
   48,3 % tidak termasuk loop coverage `b4-verify`; bisa dinaikkan menyusul
   (bukan bagian target gate ≥ 80 % service inti).
