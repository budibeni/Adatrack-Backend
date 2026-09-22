# Deploy ke Coolify — Sekali Klik (PRD §7, §14.1, §14.5, §14.6)

> Dokumen ini menjawab satu hal: **dari repo ke production tanpa langkah manual di server.**

## 1. Di mana file composnya?

```
backend/deployments/docker-compose.coolify.yml         ← SATU file deploy (16 service)
backend/.env.coolify                                   ← sumber nilai environment (gitignored)
backend/monitoring/prometheus/prometheus.coolify.yml   ← scrape config khusus container
```

Isi stack: **infra** (`postgres`, `redis`, `nats`) + **6 service aplikasi**
(`ingestion-tcp`, `worker-live`, `worker-persistence`, `worker-alert`,
`service-websocket`, `api-vehicle`) + **monitoring** (`prometheus`, `alertmanager`,
`grafana`, `node-exporter`, `cadvisor`, `postgres-exporter`, `redis-exporter`).

Semuanya **di dalam satu stack** — tidak ada resource eksternal yang harus dibuat
dulu, jadi deploy benar-benar satu klik.

## 2. Langkah deploy (5 menit, sekali klik di akhir)

### 2.1 Buat resource
1. Coolify → `+ New` → **Docker Compose** (Git repository) → pilih repo
   `budibeni/Adatrack-Backend` + branch `apps_new`.
2. Build Pack: **Docker Compose**. Lalu isi:

   | Field | Nilai |
   |---|---|
   | **Base Directory** | `backend` |
   | **Docker Compose Location** | `deployments/docker-compose.coolify.yml` |

3. Save → Coolify menampilkan *Docker Compose Content* (hasil parse). Semua
   `${KEY}` di situ muncul sebagai **application variables**.

### 2.2 Paste environment (sekali tempel)
Configuration → **Environment Variables** → **Developer view** → paste **seluruh
isi `backend/.env.coolify`** → Save.

Coolify memblokir deploy selama variabel bertanda `${VAR:?}` masih kosong — jadi
salah ketik/ketinggalan langsung ketahuan sebelum container naik:

| Wajib (kalau kosong deploy ditolak) | Catatan |
|---|---|
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | dipakai postgres + semua service + exporter |
| `JWT_SECRET` | **wajib ≥ 32 karakter**, harus sama untuk `service-websocket` & `api-vehicle` |
| `GRAFANA_ADMIN_PASSWORD` | ganti placeholder di `.env.coolify` dengan secret asli |

`DATABASE_URL` **sengaja boleh kosong** — DSN diturunkan dari `POSTGRES_*`.

Variabel opsional (aman dibiarkan kosong / pakai default): `WS_ALLOWED_ORIGINS`,
`SMTP_HOST` (kosong = kanal email mati), `SMS_*` (kosong = kanal SMS mati),
`API_VEHICLE_HTTP_ADDR`, `ALERT_METRICS_ADDR`, `PROM_RETENTION`, `IMAGE_TAG`.

> **Jangan** pakai `env_file: ../.env.coolify`: file itu gitignored sehingga tidak
> ikut ter-clone di server dan justru **menggagalkan** deploy. Karena itu semua
> parameter dideklarasikan eksplisit di blok `x-app-env` pada compose.

### 2.3 Isi domain (hanya yang perlu publik)
Setiap service HTTP → kolom **Domains**. Sertakan **port internal** sebagai akhiran:

| Service | Nilai contoh |
|---|---|
| `service-websocket` | `https://api.adatrackgps.com:8082` |
| `api-vehicle` | `https://api.adatrackgps.com` (port `8081`) |
| `grafana` (opsional) | `https://grafana.adatrackgps.com:3000` |

Proxy + TLS otomatis oleh Coolify. Semua service lain **internal-only**
(`expose`, tanpa port host) — sesuai rekomendasi resmi Coolify.

### 2.4 Buka port device GPS
`ingestion-tcp` adalah **satu-satunya** service yang mem-publish port host, karena
device GPS connect TCP langsung (tidak lewat proxy HTTP):

| Port | Sumber | Keterangan |
|---|---|---|
| `5001` | `TCP_PORT` | GT06 |
| `5027` | `TELTONIKA_TCP_PORT` | Teltonika |

Pastikan keduanya dibuka di firewall server.

### 2.5 Deploy
Tombol **Deploy**. Coolify: pull repo → build 6 image Go (konteks `backend/`) →
jalankan 16 container → tunggu healthcheck.

## 3. Yang berjalan otomatis (tanpa langkah manual)

- **Migrasi database (§14.5)** — `MIGRATE_ON_BOOT=true` pada `x-app-env`, jadi
  setiap service meng-apply migrasi master/company saat start. Konkurensi aman
  karena PostgreSQL **advisory lock** (`MIGRATE_LOCK_TIMEOUT_SEC`): satu migrator
  jalan, lainnya menunggu. Ledger `tm_schema_migrations` diperiksa; `/healthz`
  tidak `ok` bila migrasi belum lengkap.
- **Bootstrap schema + seed referensi** — `database/init-pg` di-mount ke
  `docker-entrypoint-initdb.d` dan artefak `database/` ikut ke dalam image.
- **Seed idempoten** — semua migrasi/seed aman diulang (`IF NOT EXISTS`,
  `ON CONFLICT DO UPDATE`).
- **Persistensi** — volume `pgdata`, `redisdata`, `natsdata`, `promdata`,
  `alertdata`, `grafanadata` (Coolify memberi prefix unik per resource).

**Opsional (hardening):** isi *Pre-deployment Command* dengan
`sh scripts/pg-wait.sh && sh scripts/migrate.sh` untuk fail-fast **sebelum**
container naik. Perlu diingat image runtime saat ini **tidak memuat client
`psql`**, jadi praktik yang direkomendasikan adalah membiarkan migrasi di jalur
boot (§14.5 step 3) seperti di atas.

## 4. Checklist verifikasi (PRD §14.6)

1. **Semua container Up/healthy** — Coolify → Deployments → lihat tiap komponen.
2. **Healthz** — dari server:
   ```bash
   curl -fsS https://api.adatrackgps.com:8082/healthz   # service-websocket
   curl -fsS https://api.adatrackgps.com/healthz        # api-vehicle
   ```
   `status: ok` dengan `postgres_master`, `tenant_pools`, `nats`, `redis` ok.
3. **Migrasi** — cek ledger:
   ```sql
   SELECT count(*) FILTER (WHERE success) AS applied,
          count(*) FILTER (WHERE NOT success) AS failed
   FROM adatrack_gps_master.tm_schema_migrations;
   ```
   `failed = 0` dan `applied` = jumlah file di `database/migrations/master_pg`.
4. **Monitoring** — Prometheus: semua target `up` (6 service + 5 exporter);
   Grafana: dashboard uid `adatrack-core` ter-provision otomatis.
5. **Smoke E2E** — kirim frame GT06/Teltonika ke `:5001`/`:5027`, pastikan ada
   baris baru di `th_telemetry_logs` dan alert live muncul di WebSocket; login
   lewat `service-websocket` lalu pakai token yang sama ke `api-vehicle`.

## 5. Deploy berikutnya

Push ke branch `apps_new` → webhook Coolify memicu deploy ulang. Tanpa langkah DB
manual: migrasi baru ikut ter-apply saat boot. Rollback = deploy image/commit
sebelumnya + restore backup (§12); migrasi bersifat **forward-only**.

## 6. Troubleshooting

| Gejala | Penyebab & solusi |
|---|---|
| `env file not found` saat deploy | Memakai versi compose lama yang memakai `env_file`. Versi ini menaruh semua parameter di blok `x-app-env`. |
| Deploy diblokir: *required variable is missing* | Ada key `${VAR:?}` yang belum diisi (`POSTGRES_*`, `JWT_SECRET`, `GRAFANA_ADMIN_PASSWORD`). Isi di Environment Variables. |
| `password authentication failed for user "..."` **setelah mengganti `POSTGRES_USER`/`POSTGRES_PASSWORD`** | Volume `pgdata` sudah ter-init dengan user lama — `POSTGRES_*` hanya dipakai saat **initdb pertama**. Perbaikan non-destruktif: login sebagai superuser lama lalu `CREATE ROLE <user> LOGIN SUPERUSER PASSWORD '<pw>'` (+ alihkan kepemilikan schema), atau hapus volume untuk re-init (destruktif). |
| Service `unhealthy`, log menyebut koneksi DB/NATS | `POSTGRES_HOST`/`REDIS_HOST`/`NATS_URL` harus nama service (`postgres`, `redis`, `nats`) di network stack ini. |
| Domain *No Available Server* | Tambahkan **port internal** di kolom Domains (`:8082`, `:8081`, `:3000`) dan pastikan proses listen di `0.0.0.0` (bukan `127.0.0.1`). |
| Prometheus target `down` | Port metrics diubah lewat env; samakan target di `monitoring/prometheus/prometheus.coolify.yml`. |
| Device GPS tidak connect | Port `TCP_PORT`/`TELTONIKA_TCP_PORT` belum dibuka di firewall, atau nilainya bukan 5001/5027. |

## 7. Catatan

- **Monitoring internal-only**: `prometheus`/`alertmanager`/exporter tanpa port
  host; hanya Grafana yang biasanya diberi domain.
- **Object storage (B5b / `service-media`)** belum ada di stack ini. Bila alur
  media dashcam diaktifkan, tambahkan MinIO atau pakai S3 eksternal dan isi
  `MEDIA_S3_*`.
- **Config tidak boleh campur** (§7): varian ini hanya membaca `.env.coolify`,
  jangan pernah menyisipkan nilai dari `.env.local`.
- **Magic variable Coolify** (opsional, butuh Coolify ≥ v4.0.0-beta.411): untuk
  domain otomatis tanpa mengisi kolom Domains, deklarasikan
  `SERVICE_FQDN_<SERVICE>_<PORT>` pada environment service yang bersangkutan
  (mis. `SERVICE_FQDN_SERVICE_WEBSOCKET_8082`). Perhatikan aturan penamaan: tanda
  hubung pada nama service dipertahankan agar port bisa diparse.
