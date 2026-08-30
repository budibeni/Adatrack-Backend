# Deploy Adatrack Backend ke Coolify (Staging)

Panduan praktis men-deploy **seluruh backend Adatrack** (infra + 7 service Go)
ke Coolify dengan **satu resource Docker Compose** dari branch `staging`.

Repo: `git@github.com:arfian107/adatrack_backend.git` (branch: `staging`)

---

## 1. Apa yang akan berjalan

`docker-compose.staging.yml` → **11 container** saat `docker compose up`:

| Grup | Service | Port container | Keterangan |
|---|---|---|---|
| DB | `postgres` (default) | 5432 | Provider default (PRD §7.1.1) |
| DB (opsi) | `mysql` | 3306 | Hanya aktif bila `DATABASE_PROVIDER=mysql` + `COMPOSE_PROFILES=mysql` |
| Cache | `redis` | 6379 | Live-state + cache IMEI |
| Bus | `nats` | 4222 / 8222 | JetStream + health |
| Object store | `minio` | 9000 / 9001 | S3-compatible utk service-media |
| App | `ingestion-tcp` | 9000/9001/9002 + 8090 | GT06 / Teltonika / TK103 |
| App | `worker-live` | 8092 | Live-state → Redis |
| App | `worker-persistence` | 8091 | Batch insert telemetry_logs |
| App | `worker-alert` | 8093 | Geofence / speed / SOS / fuel |
| App | `service-websocket` | 8080 / 9090 | REST + WebSocket + RBAC |
| App | `api-vehicle` | 8081 | CRUD vehicle / routes |
| App | `service-media` | 8095 / 9091 | Dashcam event media |

Build context = **root repo** (monorepo: semua Dockerfile butuh `../../internal`).
`.dockerignore` menjaga binary/secret tidak ikut image.

---

## 2. Prasyarat

- Server dengan Docker + akses ke Coolify (v4+).
- Repo GitHub ini sudah punya SSH deploy key / public access (Coolify GitHub App paling mudah).
- Domain (opsional) untuk `service-websocket`, `api-vehicle`, `service-media`.

---

## 3. Environment Variables (WAJIB diisi di Coolify)

Tidak ada file `.env` di git. Semua nilai di-interpolasi dari **Environment
Variables** resource Coolify. Ganti placeholder `GANTI-...`:

| Variabel | Contoh | Wajib? |
|---|---|---|
| `JWT_SECRET` | `$(openssl rand -hex 32)` | ✅ (sama utk websocket & api-vehicle) |
| `POSTGRES_PASSWORD` | kuat | ✅ |
| `MEDIA_S3_ACCESS_KEY` | random | ✅ (service-media) |
| `MEDIA_S3_SECRET_KEY` | random | ✅ |
| `MEDIA_DEFAULT_HMAC_SECRET` | random | ✅ |
| `COMPOSE_PROFILES` | (kosong / `mysql`) | hanya utk MySQL |
| `DATABASE_PROVIDER` | `postgres` (default); `mysql` utk MySQL | ✅ |
| `CORS_ALLOWED_ORIGINS` | `https://app.anda.id` | untuk domain frontend |

Opsional lain (nilai default aman): `LOG_LEVEL`, `SPEED_LIMIT`, `NATS_SUBJECT_PREFIX`,
`TCP_PORT`, `POSTGRES_POOL_MIN/MAX`, `MEDIA_S3_BUCKET`, `MEDIA_MAX_FILE_MB`, dsb.

> Bila password DB mengandung karakter khusus (`@ : / #`), set `DATABASE_URL`
---

## 4. Langkah Coolify (klik-by-klik)

### 4.1 Hubungkan GitHub
1. **Sources → GitHub → Install / Continue GitHub App**.
2. Authorize repo `arfian107/adatrack_backend` (atau seluruh org).
3. Coolify otomatis membuat webhook → **auto-deploy** tiap push `staging`.

### 4.2 Buat resource Docker Compose
1. **+ Add Resource → Docker Compose**.
2. **Sumber:** pilih repo GitHub `adatrack_backend`, **Branch:** `staging`.
3. **Path file compose:** `docker-compose.staging.yml` (berada di root repo).
4. **Environment Variables** → tempel isi §3 (jangan lupa secret).
5. **Persistent storage:** rancu — compose sudah mendefinisikan volume
   (`postgres_data`, `redis_data`, `nats_data`, `minio_data`, `mysql_data`).
   Pastikan Coolify memakai *volume named* (bukan ephemeral) supaya data
   tidak hilang saat redeploy.
6. Simpan & **Deploy**.

### 4.3 Domain & expose port (opsional tapi disarankan)
- HTTP didepan reverse-proxy Coolify (HTTPS otomatis via Let's Encrypt):
  - `service-websocket` → domain utama (REST + WS `/api/...`, `/ws`).
  - `api-vehicle` → subdomain (mis. `api.<domain>`).
  - `service-media` → subdomain (mis. `media.<domain>`).
- Port TCP device GPS TIDAK melalui HTTPS — tetap di-expose port server:
  - `ingestion-tcp`: **9000** (GT06), **9001** (Teltonika), **9002** (TK103).
  - Tambahkan firewall hanya untuk IP device yang dikenal (anti-spoofing tetap
    aktif via allowlist IMEI di `master.vehicle_imei_map`).

---

## 5. Verifikasi setelah deploy

```bash
# healthz tiap service (ganti host/domain sesuai)
curl -s http://<host>:8090/healthz   # ingestion-tcp (DB + NATS)
curl -s http://<host>:8091/healthz   # worker-persistence
curl -s http://<host>:8092/healthz   # worker-live (Redis + NATS)
curl -s http://<host>:8093/healthz   # worker-alert
curl -s https://<domain>/healthz                   # service-websocket
curl -s https://api.<domain>/healthz               # api-vehicle
curl -s https://media.<domain>/healthz             # service-media

# NATS internal
docker exec adatrack_gps_staging-nats-1 curl -s http://127.0.0.1:8222/healthz
```

E2E singkat (dari `tools/e2e-pub`):
```bash
go run ./tools/e2e-pub -host <host>:4222 -cases <file>.json
```

---

## 6. Troubleshooting umum

| Gejala | Kemungkinan | Solusi |
|---|---|---|
| `panic: invalid configuration` | Secret kosong / placeholder `GANTI-...` | Isi env vars §3, redeploy |
| Service restart-loop `nats:disconnected` | NATS belum healthy saat start | `depends_on` sudah di-set; tunggu, cek `docker logs nats` |
| `relation "..." does not exist` | Schema tenant belum dibentuk | Pastikan volume `postgres_data` fresh → init-pg jalan (`01_schemas.sql`, `03_company_setup.sql`); cek `COMPANY_MIGRATIONS_DIR=/db/migrations/company_pg` |
| Seed wilayah gagal | Init butuh file seed | Mount `./database/seed:/db/seed` harus ada (sudah di compose) |
| Device connect tapi tidak masuk | Port TCP belum terbuka / IMEI belum allowlist | Expose 9000-9002, daftarkan IMEI di `master.vehicle_imei_map` |
| Build lambat | Api GitHub rate-limit `go mod download` | Jalankan ulang; cache layer Coolify akan mempercepat build berikutnya |
| MySQL dipakai tapi tak jalan | Butuh profil | Set `COMPOSE_PROFILES=mysql` + `DATABASE_PROVIDER=mysql` di env Coolify |

---

## 7. Catatan repo

- **Binary hasil build lokal TIDAK di-track** (`.gitignore` + `git rm --cached`),
  dan `.dockerignore` mengecualikan binary/secret dari build context.
- Branch tersedia di remote `arfian107/adatrack_backend`:
  `staging`, `main`, `fuel_fitur_v1.1.0`, `dashcam_fitur_v1.1.0`,
  `adding_another_test_v1.1.0`.
- Update berkala: cukup `git push` branch yang dipakai Coolify → webhook
  redeploy otomatis.
> ter-URL-encode (mis. `user%40pass`) — priority tertinggi di kode.