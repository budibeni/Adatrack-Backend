# PANDUAN BACKEND — Setup & Menjalankan di Server Kosong

> Panduan langkah-demi-langkah **khusus BACKEND** untuk memasang dan menjalankan aplikasi GPS Tracking
> pada server baru yang masih kosong: apa yang harus diinstal, cara menyiapkan Docker,
> mengisi konfigurasi, menjalankan infrastruktur database/broker, menjalankan 6 service Go,
> memverifikasi sistem, hingga backup & troubleshooting.
>
> **Lingkup BAB INI EXCLUSIVE BACKEND.** Frontend (Next.js/dashboard aplikasi) berada di luar
> cakupan — tidak akan dibahas di sini. Yang dijelaskan hanyalah sisi server: REST API + WebSocket
> backend yang siap dikonsumsi klien/dashboard nanti.
>
> Target pembaca: tim operasional yang akan men-deploy dari nol. Asumsi: server **Ubuntu/Debian**
> x86_64 dengan akses `root` (atau `sudo`), belum ada aplikasi apa pun.

---

## Daftar Isi

1. [Ringkasan & Hasil Akhir](#1-ringkasan--hasil-akhir)
2. [Spesifikasi Server & Persiapan](#2-spesifikasi-server--persiapan)
3. [Instalasi Perangkat Lunak](#3-instalasi-perangkat-lunak)
4. [Clone Repository](#4-clone-repository)
5. [Konfigurasi `backend/.env`](#5-konfigurasi-backendenv)
6. [Menjalankan Infrastruktur Docker](#6-menjalankan-infrastruktur-docker)
7. [Build 6 Service Go](#7-build-6-service-go)
8. [Menjalankan Service](#8-menjalankan-service)
9. [Verifikasi & Smoke Test](#9-verifikasi--smoke-test)
10. [Load Test Pipeline (Opsional)](#10-load-test-pipeline-opsional)
11. [Monitoring Stack (Opsional)](#11-monitoring-stack-opsional)
12. [High Availability / Replica (Opsional)](#12-high-availability--replica-opsional)
13. [Backup & Restore](#13-backup--restore)
14. [Operasional Harian](#14-operasional-harian)
15. [Troubleshooting Umum](#15-troubleshooting-umum)
16. [Daftar Port](#16-daftar-port)
17. [Referensi](#17-referensi)

---

## 1. Ringkasan & Hasil Akhir

Aplikasi terdiri dari **2 bagian** yang harus berjalan bersamaan:

1. **Infrastruktur (via Docker)** — satu file `docker-compose.yml` men-start:
   - **Database PostgreSQL** (default) atau **MySQL 8.0** — penyimpanan permanen.
   - **Redis** — state live device.
   - **NATS JetStream** — message broker antar service.
2. **6 service Go** — binary yang berjalan di server (bisa langsung di host atau via Docker):
   - `ingestion-tcp` — penerima data dari perangkat GPS (TCP port 9000, protokol GT06 dll).
   - `worker-live` — menulis posisi/status realtime ke Redis.
   - `worker-persistence` — menyimpan telemetry ke database (batch).
   - `worker-alert` — deteksi geofence / overspeed / SOS / offline / rute.
   - `service-websocket` — REST API backend + WebSocket (port 8080) yang dipakai dashboard/klien.
   - `api-vehicle` — REST API CRUD kendaraan & konfigurasi (port 8081).

**Hasil akhir yang diharapkan** setelah panduan selesai:

```
Infra (docker)            Service (host)              Endpoint yang hidup
┌─────────────┐           ┌──────────────────┐        • http://<ip>:8080/healthz
│ postgres/mysql│ ───────► │ ingestion-tcp    │        • http://<ip>:8081/healthz
│ redis        │ ◄─────── │ worker-live      │        • TCP <ip>:9000 (device GPS)
│ nats         │ ◄─────── │ worker-persistence│
└─────────────┘           │ worker-alert     │
                          │ service-websocket │       Data GPS masuk →
                          │ api-vehicle      │       MySQL/Postgres bertambah
                          └──────────────────┘       Redis berisi live state
```

---

## 2. Spesifikasi Server & Persiapan

### 2.1 Rekomendasi spesifikasi

| Item | Minimal | Ideal |
|---|---|---|
| CPU | 2 core | 4+ core |
| RAM | 4 GB | 8 GB |
| Disk | 30 GB SSD | 100 GB+ SSD (data telemetry bertambah ~1–2 GB/bulan untuk 1000 device) |
| OS | Ubuntu 22.04 / 24.04 (64-bit) | — |

### 2.2 Port yang harus dibuka (firewall / security group)

| Port | Keperluan |
|---|---|
| **9000** | TCP dari perangkat GPS (protokol GT06) — **paling penting** |
| 9001 / 9002 | (opsional) Teltonika / TK-103 |
| 8080 | REST API + WebSocket `service-websocket` |
| 8081 | REST API `api-vehicle` |
| 3000 / 9090 / 9093 | (opsional) Grafana / Prometheus |
| 5432 (atau 5532) / 3307 | (opsional, hanya saat set-up/inspeksi) akses DB |
| 22 | SSH admin |

Untuk Ubuntu dengan `ufw`:

```bash
sudo ufw allow 22/tcp
sudo ufw allow 9000/tcp          # device GPS
sudo ufw allow 8080/tcp          # API utama/WS
sudo ufw allow 8081/tcp          # API vehicle
sudo ufw allow 9001/tcp 9002/tcp # protokol lain (opsional)
sudo ufw enable
```

### 2.3 Persiapan dasar

```bash
# Update sistem & pasang tool dasar
sudo apt-get update && sudo apt-get upgrade -y
sudo apt-get install -y ca-certificates curl gnupg git

# Pastikan waktu server akurat (penting untuk telemetry & JWT)
sudo timedatectl set-timezone Asia/Jakarta   # sesuaikan
sudo apt-get install -y chrony               # atau ntpsec; pastikan "System clock synchronized: yes"
```

---

## 3. Instalasi Perangkat Lunak

### 3.1 Docker Engine + Docker Compose plugin

```bash
# 1) Tambah repository resmi Docker
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
sudo chmod a+r /etc/apt/keyrings/docker.gpg

echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu \
  $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# 2) Install Docker Engine + Compose plugin
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# 3) Verifikasi
docker --version            # contoh: Docker version 26.x
docker compose version      # contoh: Docker Compose version v2.x
sudo docker run --rm hello-world   # tes: harus sukses

# 4) (Opsional) agar user non-root bisa menjalankan docker tanpa sudo
sudo usermod -aG docker $USER
newgrp docker               # atau logout-login
```

### 3.2 Go (untuk build/jalankan service langsung)

```bash
# Ambil versi sesuai go.mod tiap service (saat ini go 1.25)
GO_VER=1.25.0
curl -fsSL "https://go.dev/dl/go${GO_VER}.linux-amd64.tar.gz" -o /tmp/go.tgz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tgz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
go version                 # go version go1.25.0 linux/amd64
```

### 3.3 Alat bantu lain (disarankan)

```bash
sudo apt-get install -y jq htop tmux
```

> Jika ingin menjalankan service sebagai **container** (bukan langsung di host), Go tidak wajib
> diinstal di server — binary dibangun lewat Dockerfile (lihat §8.3). Panduan utama di bawah
> memakai metode **host** karena itu konvensi repo (services menyambung ke infra via port host).

---

## 4. Clone Repository

```bash
cd /opt                                   # atau direktori lain milik user
sudo git clone <url-repo-ajb_gps> ajb_gps
sudo chown -R $USER:$USER /opt/ajb_gps    # agar bisa diedit tanpa sudo
cd /opt/ajb_gps

# (Opsional) cek status roadmap/setup — versi terbaru sudah berisi backend lengkap
ls backend/
```

Jika repo tidak tersedia/private, salin folder `backend/` dari sumber yang sudah ada.

**Struktur penting yang akan dipakai:**

```
backend/
├── .env.example               # template konfigurasi → salin ke .env
├── docker-compose.yml         # infra utama (DB + Redis + NATS)
├── internal/                  # library bersama (tidak perlu diotak-atik)
├── database/                  # migrasi + seed otomatis saat DB pertama kali hidup
├── services/                  # 6 service Go (masing-masing punya go.mod)
├── deployments/               # overlay monitoring & HA (opsional)
├── scripts/                   # helper: compose-up, backup, loadtest dll
├── loadtest/                  # simulator perangkat GPS untuk tes
└── cmd/                       # tool tambahan (querybench, dll)
```

---

## 5. Konfigurasi `backend/.env`

Semua service memakai **satu file env** `backend/.env`. Buat dari template:

```bash
cd /opt/ajb_gps/backend
cp .env.example .env
nano .env      # atau vim: isi sesuai bagian-bagian di bawah
```

> ⚠️ `backend/.env` berisi password/secret — **jangan pernah di-commit** ke git.
> `backend/.gitignore` sudah mengecualikannya.

### 5.1 Variabel yang WAJIB disesuaikan

| Variabel | Isi dengan | Contoh |
|---|---|---|
| `DATABASE_PROVIDER` | engine DB: `postgres` (default) atau `mysql` | `postgres` |
| `COMPOSE_PROFILES` | **sama dengan** `DATABASE_PROVIDER` | `postgres` |
| `POSTGRES_PASSWORD` | password database | `P@ssw0rd!` |
| `POSTGRES_ROOT_PASSWORD` | password user root DB | `R00t@123` |
| `MYSQL_PASSWORD` / `MYSQL_ROOT_PASSWORD` | (bila provider=mysql) | — |
| `JWT_SECRET` | string acak panjang minimal 32 karakter | `openssl rand -hex 32` |
| `DATABASE_URL` | DSN Postgres; **password harus di-URL-encode** (`@` → `%40`) | lihat §5.2 |
| `POSTGRES_REPLICA_PASSWORD` | bila memakai replika (§12) | — |
| `REDIS_PASSWORD` | biarkan kosong bila Redis tanpa auth | — |

Buat secret acak:

```bash
openssl rand -hex 32     # untuk JWT_SECRET
```

### 5.2 Contoh isian `DATABASE_URL`

Format DSN yang diharapkan **PostgreSQL**:

```
postgres://<user>:<password-url-encoded>@<host>:<port>/<db>?sslmode=disable
```

Jika `POSTGRES_USER=adatrack_gps_user`, `POSTGRES_PASSWORD=user@gps2608`,
`POSTGRES_PORT=5432`, `POSTGRES_DB=adatrack_gps_db`, maka:

```
DATABASE_URL=postgres://adatrack_gps_user:user%40gps2608@127.0.0.1:5432/adatrack_gps_db?sslmode=disable
```

> **Penting:** karakter `@` di password harus diubah menjadi `%40`. Karakter istimewa lain
> (`:`, `/`, `#`, `%`, dll.) juga perlu di-URL-encode.
> Service memakai `search_path` database saat build DSN → `DATABASE_URL` utama hanya sebagai
> preferensi host/db (default di `internal/config`).

### 5.3 Port infra (pastikan konsisten)

Untuk docker-compose, beberapa port host **harus diisi eksplisit** di `.env` agar bisa
diprediksi (variabel ini dipakai interpolasi `docker-compose.yml`):

```env
# === NATS ===
NATS_CLIENT_PORT=4222        # client broker
NATS_HEALTHZ_PORT=8222       # HTTP monitoring NATS

# === PostgreSQL ===
POSTGRES_PORT=5432           # port host PostgreSQL  (bila 5432 terpakai, gunakan 5532)

# === MySQL (bila provider=mysql) ===
MYSQL_PORT=3307              # port host MySQL

# === Redis ===
REDIS_PORT=6390              # port host Redis       (6379 sering terpakai host-native)

# === Koneksi service → infra (host yang sama) ===
NATS_URL=127.0.0.1:4222
REDIS_ADDR=127.0.0.1:6390
MYSQL_HOST=127.0.0.1
POSTGRES_HOST=127.0.0.1
```

Semua nilai host memakai `127.0.0.1` karena **infra di Docker, service di host yang sama**.
Di penyebaran container (semua di Docker), gunakan nama service (`postgres`, `redis`, `nats`).

### 5.4 Variabel lain yang boleh disesuaikan

| Variabel | Default | Keterangan |
|---|---|---|
| `TCP_PORT` | `9000` | port listener perangkat GPS |
| `TELTONIKA_TCP_PORT` / `TK103_TCP_PORT` | `9001` / `9002` | `0` = nonaktif |
| `SPEED_LIMIT` / `SPEED_GRACE_MARGIN` | `80` / `10` | limit kecepatan global default |
| `SOS_ESCALATION_MINUTES` / `SOS_ESCALATION_MAX` | `2` / `3` | eskalasi SOS |
| `JWT_EXPIRY_HOURS` | `24` | umur token |
| `RATE_LIMIT_LOGIN_ATTEMPTS` / `RATE_LIMIT_LOGIN_WINDOW_MIN` | `5` / `15` | proteksi login |
| `LOG_LEVEL` | `info` | `debug` saat ingin log detail |
| `TLS_ENABLED` | `false` | `true` bila memakai HTTPS/WSS + sertifikat |
| `DB_REPLICA_ENABLED` | `false` | `true` **hanya** bila replika ikut jalan (§12) |

> **Catatan penting:** skrip bantu (mis. `scripts/verify-endurance.sh`) memakai asumsi penamaan
> DB `adatrack_gps_{tenant}` dan tenant seed `dev001`. Jika Anda mengubah `COMPANY_DB_PREFIX`
> atau isi `.env` secara drastis, selaraskan juga skrip/tool yang dipakai.

---

## 6. Menjalankan Infrastruktur Docker

### 6.1 Start (menggunakan helper resmi)

```bash
cd /opt/ajb_gps/backend
./scripts/compose-up.sh up -d
```

`compose-up.sh` otomatis:
1. membaca `DATABASE_PROVIDER` dari `.env`;
2. memvalidasi nilainya (`postgres` | `mysql`);
3. men-set `COMPOSE_PROFILES=<provider>` → hanya DB terpilih yang ikut start;
4. meneruskan perintah ke `docker compose` dengan `--env-file .env`.

Alternatif manual (harus memastikan `COMPOSE_PROFILES` sudah benar di `.env`):

```bash
cd /opt/ajb_gps/backend
docker compose --env-file .env up -d
```

### 6.2 Tunggu hingga healthy

**Penting:** saat volume DB **pertama kali** dibuat, container akan menjalankan migrasi +
seed otomatis (membuat master DB, tenant `default`, tabel partisi, data wilayah 250 negara s/d
83 ribu desa). Ini butuh **waktu** (1–5 menit). Jangan restart container di tengah proses.

```bash
./scripts/compose-up.sh ps
```

Tunggu sampai status semua `healthy`:

```
NAME       IMAGE                STATUS
postgres   postgres:16-alpine   Up X seconds (healthy)
redis      redis:7-alpine       Up X seconds (healthy)
nats       nats:2.10-alpine     Up X seconds (healthy)
```

Poll dengan perintah:

```bash
watch -n 5 "./scripts/compose-up.sh ps"     # Ctrl+C untuk keluar
```

### 6.3 Verifikasi isi database (PostgreSQL)

```bash
docker exec -it postgres psql -U adatrack_gps_user -d adatrack_gps_db -c "\l"
# Harus terlihat: adatrack_gps_db
docker exec -it postgres psql -U adatrack_gps_user -d adatrack_gps_db \
  -c "SELECT count(*) FROM adatrack_gps_master.users;"
# Di jalur PG, master/tenant adalah SCHEMA di dalam satu database.
```

### 6.3b Verifikasi isi database (MySQL)

```bash
docker exec -it mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e "SHOW DATABASES;"
# Harus terlihat: adatrack_gps_master, adatrack_gps_default, ...
docker exec -it mysql mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -e \
  "SELECT COUNT(*) FROM adatrack_gps_master.users;"
```

### 6.4 Menghentikan infrastruktur

```bash
./scripts/compose-up.sh down        # hentikan container (data aman di volume)
./scripts/compose-up.sh down -v     # ⚠️ HAPUS data! Hanya untuk reset total
```

### 6.5 Ganti provider database

```bash
./scripts/compose-up.sh down                 # 1) stop dulu
# 2) ubah DATABASE_PROVIDER & COMPOSE_PROFILES di .env
# 3) hapus/lihat volume DB provider lama bila ingin mulai bersih; volume provider target
#    yang belum pernah ada akan di-seed otomatis saat `up` pertama.
./scripts/compose-up.sh up -d
```

> Data **tidak** bermigrasi otomatis antar engine. Ganti provider = database baru kosong
> (perlu disiapkan ulang skema + seed).

---

## 7. Build 6 Service Go

Setiap service adalah module Go terpisah (`ajb_gps/<nama>`) dan memakai library bersama
`ajb_gps/internal` lewat `replace` — jadi build **wajib** dilakukan dari area repo `backend`
(atau dari direktori service itu sendiri). Build mula-mula akan men-download module
(butuh internet; sebaiknya GOFLAGS `-mod=mod` aman — lihat §15 jika koneksi terbatas).

```bash
cd /opt/ajb_gps/backend/services

for svc in ingestion-tcp worker-live worker-persistence worker-alert service-websocket api-vehicle; do
  echo "==> build $svc"
  ( cd "$svc" && go build -o "./$svc" . ) || { echo "build $svc GAGAL"; break; }
done

ls -lh ingestion-tcp/ingestion-tcp worker-live/worker-live \
       worker-persistence/worker-persistence worker-alert/worker-alert \
       service-websocket/service-websocket api-vehicle/api-vehicle
```

Setiap service juga berisi **unit test** — jalankan sekali untuk memastikan kode sehat:

```bash
cd /opt/ajb_gps/backend/internal  && go test ./... && cd ..
cd /opt/ajb_gps/backend/services && for svc in *; do
  [ -f "$svc/go.mod" ] && ( cd "$svc" && go test ./... )
done
```

> **Catatan:** gunakan Go **1.25** (sesuai `go.mod`). Versi lebih lama dapat gagal.

---

## 8. Menjalankan Service

### 8.1 Prinsip & urutan

- Service membaca `.env` secara otomatis (walk-up), jadi **jalankan dari dalam folder**
  `backend/` (beberapa service memakai path relatif ke cert — lihat `TLS_ENABLED`).
- **Urutan start:** infra Docker harus sudah `healthy` → baru service. Antar service sendiri,
  tidak ada dependensi keras saat boot (retry + backoff internal yang handle NATS/DB belum siap),
  tetapi urutan berikut paling aman:

```
1. ingestion-tcp        → mulai terima data dari device (TCP 9000)
2. worker-live          → siap menulis Redis
3. worker-persistence   → siap menyimpan ke DB
4. worker-alert         → siap deteksi alert
5. service-websocket    → REST + WS backend siap dipakai dashboard/klien
6. api-vehicle          → REST CRUD fleet
```

- Tiap service menangkap **SIGTERM** untuk graceful shutdown (drain buffer & settle ack),
  jadi gunakan `kill <pid>` (atau systemd `stop`) — hindari `kill -9` di tengah beban.

### 8.2 Cara host (foreground vs background)

**Foreground (untuk uji coba / melihat log langsung):**

```bash
cd /opt/ajb_gps/backend/services/ingestion-tcp
./ingestion-tcp
```

**Background semua service sekaligus:**

```bash
cd /opt/ajb_gps/backend/services

cat > /tmp/start-backend.sh <<'EOF'
#!/usr/bin/env bash
# Pindah ke BACKEND_DIR (lokasi backend/.env di-load oleh service saat walk-up)
cd "$(dirname "$0")/../.."
for svc in ingestion-tcp worker-live worker-persistence worker-alert service-websocket api-vehicle; do
  ( cd "services/$svc" && nohup "./$svc" >> "/tmp/${svc}.log" 2>&1 & echo "$!" > "/tmp/${svc}.pid" )
  echo "started $svc -> /tmp/${svc}.log (pid $(cat /tmp/${svc}.pid))"
done
EOF
chmod +x /tmp/start-backend.sh
/tmp/start-backend.sh
```

Cek proses & log:

```bash
ps aux | grep -E 'ingestion-tcp|worker-|service-websocket|api-vehicle' | grep -v grep
tail -f /tmp/service-websocket.log
```

Stop bersih (kirim SIGTERM — graceful):

```bash
for svc in api-vehicle service-websocket worker-alert worker-persistence worker-live ingestion-tcp; do
  [ -f "/tmp/${svc}.pid" ] && kill "$(cat /tmp/${svc}.pid)" 2>/dev/null || true
done
```

> **Tips produksi:** gunakan **systemd unit** (lihat bagan di bawah) agar service auto-restart
> saat server reboot / crash. Contoh unit ringkas:

```
# /etc/systemd/system/ingestion-tcp.service
[Unit]
Description=adatrack ingestion-tcp
After=docker.service network-online.target
Requires=docker.service

[Service]
WorkingDirectory=/opt/ajb_gps/backend
ExecStart=/opt/ajb_gps/backend/services/ingestion-tcp/ingestion-tcp
Restart=always
RestartSec=3
UMask=0077

[Install]
WantedBy=multi-user.target
```

Buat unit untuk keenam service (sesuaikan `ExecStart`), lalu:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ingestion-tcp worker-live worker-persistence worker-alert \
                      service-websocket api-vehicle
```

### 8.3 Cara container (alternatif)

Infra sudah berjalan di Docker; service juga bisa di-container-kan (build context **wajib**
`backend/`):

```bash
cd /opt/ajb_gps/backend
docker build -f services/ingestion-tcp/Dockerfile        -t adatrack-ingestion-tcp .
docker build -f services/worker-live/Dockerfile          -t adatrack-worker-live .
docker build -f services/worker-persistence/Dockerfile   -t adatrack-worker-persistence .
docker build -f services/worker-alert/Dockerfile         -t adatrack-worker-alert .
docker build -f services/service-websocket/Dockerfile    -t adatrack-service-websocket .
docker build -f services/api-vehicle/Dockerfile          -t adatrack-api-vehicle .
```

Lalu run masing-masing dengan `--network host` (agar langsung melihat infra di port host)
dan `--env-file .env`:

```bash
docker run -d --name ingestion-tcp --network host --env-file .env adatrack-ingestion-tcp
# ... ulangi untuk service lain
```

> Saat memakai `--network host`, port bind di `.env` (`:8080`, `:8090`...) berlaku langsung
> di host. Tanpa host-network, gunakan `-p` dan ubah variabel bind/connect ke nama container.

---

## 9. Verifikasi & Smoke Test

### 9.1 Endpoint health semua service

```bash
# Service API
curl -s http://127.0.0.1:8080/healthz && echo    # service-websocket
curl -s http://127.0.0.1:8081/healthz && echo    # api-vehicle

# Worker (health + metrics)
curl -s http://127.0.0.1:8090/healthz && echo;   # ingestion-tcp
curl -s http://127.0.0.1:8091/healthz && echo;   # worker-persistence
curl -s http://127.0.0.1:8092/healthz && echo;   # worker-live
curl -s http://127.0.0.1:8093/healthz && echo;   # worker-alert

# Semua harus mengembalikan {"status":"ok"} (atau yang sejenis sesuai implementasi).
```

> Jika ada yang gagal: baca log `*.log` (service) dan `docker compose logs` (infra). Rincian
> penyebab umum di §15.

### 9.2 Tes login REST API

```bash
# Login dengan akun seed (cek dulu isi seed; contoh ada di database/seed atau
# akun platform dev: platform@adatrackgps.local)
curl -s -X POST http://127.0.0.1:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"platform@adatrackgps.local","password":"Platform@123"}'
```

Sukses → respons berisi `access_token`, `refresh_token`, profile user (`company_code`, `role`).

Pakai token untuk akses endpoint terproteksi:

```bash
TOKEN=<access_token_dari_response>
curl -s http://127.0.0.1:8080/api/v1/vehicles -H "Authorization: Bearer $TOKEN"
```

### 9.3 Tes masuknya data dari perangkat (TCP GT06)

Simulasikan satu frame login GT06 dengan `nc` (device IMEI harus terdaftar di
`master.vehicle_imei_map` — beberapa IMEI seed `dev001` akan otomatis ter-registrasi):

```bash
# IMEI terdaftar contoh (seed dev001): 864201040512345
# Frame login GT06 VALID (format identik dengan backend/loadtest buildLogin):
#   78 78 <len=0x16> 01 <IMEI ASCII 15-byte> 81 60 <timezone 00 00 01 00> <CRC-ITU 83 3C> 0d 0a
# CRC dihitung dengan algoritma crc16.go yang sama seperti parser; untuk IMEI lain
# hitung ulang CRC-nya atau langsung pakai backend/loadtest agar dijamin valid.
printf '\x78\x78\x16\x01\x38\x36\x34\x32\x30\x31\x30\x34\x30\x35\x31\x32\x33\x34\x35\x81\x60\x00\x00\x01\x00\x83\x3c\x0d\x0a' \
  | timeout 5 nc 127.0.0.1 9000
# Respons sukses = ACK login: 78 78 <len> 01 <status> ... 0d 0a
```

Catatan:
- Response **ACK login** menandakan device diterima oleh `ingestion-tcp` (autentikasi + anti-spoofing
  lolos). Frame login **tidak** menghasilkan posisi/koneksi Redis.
- Untuk melihat data benar-benar mengalir (key live-state di Redis + baris `telemetry_logs` di
  database), kirim **frame posisi** — cara termudah: jalankan `loadtest` (§10) yang mengirim
  login + posisi sekaligus. Contoh: `./loadtest -devices 1 -rate 1 -duration 10s`.

Cek Redis live-state (setelah ada frame posisi):

```bash
docker exec -it redis redis-cli GET 'adatrack_gps:dev001:vehicle:state:864201040512345'
# Harus ada JSON posisi/status (atau gunakan pola key sesuai REDIS_KEY_PREFIX di .env:
# {prefix}{company}:vehicle:state:<IMEI>)
```

dan cek database bertambah:

```bash
# PostgreSQL
docker exec -it postgres psql -U adatrack_gps_user -d adatrack_gps_db -tAc \
  "SELECT count(*) FROM adatrack_gps_dev001.telemetry_logs;"
```

> `nc` (netcat) perlu diinstall: `sudo apt-get install -y netcat-openbsd`. Frame di atas adalah
> frame login-basic; untuk pengujian data posisi yang valid gunakan `backend/loadtest` (§10)
> agar CRC/lat-lon selalu sesuai parser.

### 9.4 Tes WebSocket (backend)

```bash
# Handshake WS dengan token (lihat format pesan di implementasi/dokumen WS)
curl -i -N -H "Connection: Upgrade" -H "Upgrade: websocket" \
     -H "Sec-WebSocket-Version: 13" -H "Sec-WebSocket-Key: x3JJHMbDL1EzLkh9GBhXDw==" \
     "http://127.0.0.1:8080/api/v1/ws?token=$TOKEN"
```

Jika `101 Switching Protocols` → WebSocket aktif.

---

## 10. Load Test Pipeline (Opsional)

Untuk memastikan pipeline tidak kehilangan data, gunakan generator `backend/loadtest`
(frame GT06 **valid** sesuai parser, IMEI seed terdaftar):

```bash
cd /opt/ajb_gps/backend/loadtest
go build -o loadtest .
./loadtest -devices 20 -rate 20 -duration 60s     # = 400 msg/s selama 60 s
```

Loaded test menyuntikkan langsung ke TCP ingestion. Bandingkan jumlah frame terkirim vs
delta baris `telemetry_logs`:

```bash
BEFORE=$(docker exec -it postgres psql -U adatrack_gps_user -d adatrack_gps_db -tAc \
  "SELECT count(*) FROM adatrack_gps_dev001.telemetry_logs;")
./loadtest -devices 20 -rate 20 -duration 60s
AFTER=$(docker exec -it postgres psql -U adatrack_gps_user -d adatrack_gps_db -tAc \
  "SELECT count(*) FROM adatrack_gps_dev001.telemetry_logs;")
echo "delta = $((AFTER - BEFORE)) baris (harus = jumlah frame terkirim)"
```

**Suite sudah disediakan** (`scripts/loadtest-suite.sh`) — otomatis memilih engine
(postgres/mysql), menjalankan baseline + peak + endurance, lalu verifikasi delta:

```bash
cd /opt/ajb_gps/backend
./scripts/loadtest-suite.sh                    # butuh infra + 6 service hidup
```

---

## 11. Monitoring Stack (Opsional)

Stack observasi (Prometheus + Grafana + node/cAdvisor + DB exporter) bisa dijalankan sebagai
overlay **sambil** infra utama tetap up.

```bash
cd /opt/ajb_gps/backend
./scripts/compose-up.sh -f deployments/docker-compose.monitoring.yml up -d
```

> `compose-up.sh` akan men-set `COMPOSE_PROFILES` dari `.env` sehingga exporter DB yang sesuai
> (mysqld vs postgres) ikut up; exporter "mengikuti" provider DB.

Healthcheck:

```bash
sleep 30
curl -s http://127.0.0.1:9090/-/healthy && echo      # Prometheus
```

Interface:
- **Prometheus** : `http://<ip>:9090`
- **Grafana** : `http://<ip>:3000` (default `admin` / password lihat `docker-compose.monitoring.yml`,
  contoh `admin123`). Dashboard **"adatrack Core"** sudah ter-provisioning.
- **Alertmanager** : `http://<ip>:9093`

Rules yang terpasang (PRD §8.3): pending NATS >90%, insert latency >10 s, tenant resolution
>500 ms, pool exhaustion >90%, IMEI lookup error >5%, TCP rate, WS latency WARNING; plus
**SLO** uptime ≥99.9% (error budget + burn-rate) di `monitoring/slo-rules.yml`.

> **Perhatian port bentrok:** `cadvisor` memakai host `8080` yang juga dipakai
> `service-websocket`. Bila keduanya berjalan bersamaan, ubah mapping port `cadvisor`
> (mis. `8080` → `8082`) di `docker-compose.monitoring.yml`, atau ganti
> `WEBSOCKET_HTTP_ADDR` service. Demikian pula `node-exporter` (9100), `prometheus` (9090),
> `mysqld-exporter` (9104) harus bebas.

---

## 12. High Availability / Replica (Opsional)

Model yang dipakai: **primary = satu-satunya jalur tulis**, **replica = skala baca**. Replica
per engine (mengikuti `DATABASE_PROVIDER`):

| Replica | Karakteristik | Skrip setup |
|---|---|---|
| `postgres-replica` | streaming WAL + slot `pg_replica_slot` | `scripts/replication/setup-postgres-replication.sh` |
| `mysql-replica` | GTID, read-only | `scripts/replication/setup-mysql-replication.sh` |
| `redis-replica` | REPLICAOF | `scripts/replication/drill-redis-failover.sh`, `promote-redis-replica.sh` |

### 12.1 Lift replica

```bash
cd /opt/ajb_gps/backend
# Layani overlay HA (primary infra harus sudah up)
COMPOSE_PROFILES=<provider> \
  docker compose -f docker-compose.yml -f deployments/docker-compose.ha.yml \
  --env-file .env up -d

# Aktifkan read/write split di aplikasi
#   di .env: DB_REPLICA_ENABLED=true
#           (port replika ikut POSTGRES_REPLICA_PORT / MYSQL_REPLICA_PORT)
```

> Jika replika **tidak** dijalankan, pastikan `DB_REPLICA_ENABLED=false` — semua baca
> otomatis ke primary.

### 12.2 Penting

- Replica **bukan** mekanisme backup dan **bukan** failover — hanya melepas beban baca
  (laporan/analitik). Untuk proteksi data tetap pakai dump + uji restore (§13).
- Status replikasi: `scripts/replication/replication-status.sh`.

---

## 13. Backup & Restore

Skrip siap pakai di `backend/scripts/`:

```bash
# Backup semua database adatrack_gps_* (gzip + sha256 + retensi 14 hari)
./scripts/backup-mysql.sh

# Restore (verifikasi checksum → import → ringkasan row count)
./scripts/restore-mysql.sh <file>.sql.gz adatrack_gps_<target>

# Snapshot Redis (best-effort)
./scripts/backup-redis.sh
```

Jadwalkan otomatis dengan `cron` (contoh tiap 02.00):

```cron
0 2 * * * root /opt/ajb_gps/backend/scripts/backup-mysql.sh
```

> Dump hanya dari **primary**. Uji restore berkala (DR drill) — sudah pernah diverifikasi:
> backup `master` → restore ke DB uji → row count cocok.

---

## 14. Operasional Harian

| Tugas | Perintah |
|---|---|
| Lihat status infra | `./scripts/compose-up.sh ps` |
| Lihat log service | `tail -f /tmp/<service>.log` (host) atau `journalctl -u <service>` (systemd) |
| Lihat log infra | `docker compose logs --tail=100 <postgres|redis|nats>` |
| Restart satu service | systemd: `sudo systemctl restart <service>` · host: `kill $(cat /tmp/<svc>.pid)` lalu start lagi |
| Restart semua service | `docker compose restart` (infra) + restart 6 service |
| Rebuild service | §7 lalu restart proses |
| Tambah tenant baru | `POST /api/v1/companies` (token platform/SuperAdmin) |
| Pantau kenaikan data | `SELECT count(*) ... telemetry_logs` · dashboard Prometheus |

**Saat terjadi insiden** (CPU/memori tinggi, DB lambat/stuck, NATS/Redis bermasalah, service
stuck, throughput tinggi) → **ikuti `docs/INCIDENT_RUNBOOK.md`** (langkah otomatis & manual
peranutan: I1 CPU, I2 memori, I3 deadlock, I4 backlog, I5–I7 RDS, I8 Redis, I9 NATS, I10 persist,
I11 WS, I12 device mass-disconnect).

---

## 15. Troubleshooting Umum

### T1. `docker compose up` gagal / container `restarting`
- Lihat log: `docker compose logs <service>`. Pada PostgreSQL/MySQL pertama kali, init
  (migrasi + seed wilayah besar) butuh waktu lama — tunggu 5+ menit, jangan restart.
- Periksa port host: `sudo ss -tlnp | grep -E '5432|3307|6390|4222'` — jangan sampai port
  terpakai service lain (ubah port di `.env` lalu `up -d` lagi).

### T2. Service tidak bisa connect ke DB/Redis/NATS
- Pastikan infra `healthy` (`compose-up.sh ps`).
- Pastikan `NATS_URL`/`REDIS_ADDR`/`POSTGRES_HOST`/`MYSQL_HOST` menunjuk `127.0.0.1` (host)
  atau nama container (bila service di-container).
- PostgreSQL: pastikan `DATABASE_URL` password sudah di-URL-encode dan `POSTGRES_PORT` sesuai.
- Lihat log service — pesan `connection refused` = port/alamat salah; `password authentication
  failed` = password beda dengan env container.

### T3. Postgres error `relation "telemetry_logs" does not exist` / salah schema
- Service memaksa `search_path` per-tenant saat membangun DSN. Pastikan `DATABASE_URL` memakai
  host/db yang benar dan `COMPANY_DB_PREFIX` konsisten. Migrasi PG membuat schema `adatrack_gps_*`
  di dalam satu database (`POSTGRES_DB`).

### T4. IMEI device ditolak (tidak ada data masuk)
- Anti-spoofing: IMEI harus terdaftar di `master.vehicle_imei_map`. Daftarkan lewat
  `POST /api/v1/vehicles` (api-vehicle) atau insert seed. Cek log `ingestion-tcp` untuk
  `unknown IMEI`.
- Pastikan firewall membuka port 9000 dan device memakai APN/IP yang benar.

### T5. Data masuk tapi `telemetry_logs` tidak bertambah
- Cek `worker-persistence` log: mungkin retry habis → publish ke `telemetry.error.<IMEI>`.
  Periksa batas `MAX_OPEN_CONNS`, disk DB penuh, atau partisi belum dibuat.
- Cek `scripts/verify-endurance.sh` atau hitung delta seperti §10.

### T6. `go build` gagal download module (koneksi terbatas)
- Jalan offline / VPN: `go env -w GOPROXY=https://proxy.golang.org,direct` atau gunakan mod
  cache yang sudah ada. Pastikan `GOPROXY` tidak ke proxy internal yang mati.

### T7. Port 8080 bentrok dengan monitoring (cadvisor)
- Ubah mapping port `cadvisor` di `deployments/docker-compose.monitoring.yml`
  (mis. `8080:8080` → `8082:8080`) lalu `up -d` lagi.

### T8. Ingin reset total (hapus semua data)
```bash
./scripts/compose-up.sh down -v     # ⚠️ hapus volume (data hilang)
# lalu jalankan lagi dari §6.1; volume baru akan di-seed otomatis.
```

### T9. Data hilang saat ganti project name compose / unifikasi penamaan
- Volume compose ter-scope per project name. Saat memindahkan group ke `name: adatrack_gps_system`,
  **pin nama volume eksplisit** (`volumes.<x>.name:`) agar volume lama dipakai ulang; jangan
  `down -v`. Lihat §antrian unifikasi di `.clinerules/02-roadmap-overview.md` (B4).

### T10. Endurance/load test sengaja dihentikan
- Pakai `scripts/endurance-checkpoint.sh` (checkpoint) lalu `scripts/verify-endurance.sh`
  saat selesai. Pastikan tidak menurunkan service di tengah endurance.

---

## 16. Daftar Port

| Port | Dipakai untuk | Bind ke |
|---|---|---|
| 5432 (bisa 5532) | PostgreSQL (provider default) | infra `postgres` |
| 3307 | MySQL (bila provider=mysql) | infra `mysql` |
| 6390 | Redis | infra `redis` |
| 4222 / 8222 | NATS client / HTTP monitor | infra `nats` |
| 9000 | TCP GT06 (perangkat GPS) | `ingestion-tcp` |
| 9001 / 9002 | Teltonika / TK-103 | `ingestion-tcp` |
| 8090 / 8091 / 8092 / 8093 | health+metrics worker | worker service |
| 8080 | REST + WebSocket utama | `service-websocket` |
| 9090 | metrics `service-websocket` | `service-websocket` |
| 8081 | REST fleet | `api-vehicle` |
| 9090 / 3000 / 9093 / 9100 / 9104 | Prometheus / Grafana / Alertmanager / node / db-exporter | monitoring |
| 8082 (bila diubah) | cAdvisor | monitoring |

---

## 17. Referensi

| Dokumen | Isi |
|---|---|
| `docs/INCIDENT_RUNBOOK.md` | **Wajib dibaca** saat insiden (CPU, memori, DB, NATS, Redis, WS) |
| `docs/HIGH_AVAILABILITY.md` | Strategi HA/DR, failover Redis, read replica |
| `docs/POSTGRES_PROVIDER.md` | Detail provider PostgreSQL vs MySQL + keterbatasan |
| `docs/DATABASE_ARCHITECTURE.md` | Keputusan single-instance vs split, read/write split |
| `docs/device-connection-guide.md` | Panduan koneksi perangkat GPS |
| `docs/docs-device/*.md` | Manual protokol GT06/Teltonika vendor |
| `PRD_UPDATED.md` | Product Requirements (sumber kebenaran fitur) |
| `.clinerules/02-roadmap-overview.md` | Roadmap fase (fokus bagian backend B0–B4) |
| `.clinerules/03-backend-phases.md` | Detail tiap fase backend (B0–B5b) |

---

## Lampiran A — Checklist Cepat Setup Baru

- [ ] Server: OS ter-update, waktu sinkron, port 9000/8080/8081 terbuka
- [ ] Instal: Docker Engine + Compose plugin, Go 1.25, git, curl, jq
- [ ] Clone repo → `cp backend/.env.example backend/.env` → isi password + `DATABASE_URL` (URL-encode)
- [ ] `./scripts/compose-up.sh up -d` → tunggu semua `healthy` (1–5 menit saat seed pertama)
- [ ] Verifikasi DB: schema master + tenant `default` + seed ada
- [ ] Build 6 service (`go build -o ./<svc> .`)
- [ ] Start 6 service (host/systemd/container) → cek `/healthz` di 8080, 8081, 8090–8093
- [ ] Smoke test: login API → kirim frame GT06 → Redis + `telemetry_logs` bertambah
- [ ] (Opsional) Load test: `loadtest-suite.sh` / `verify-endurance.sh`
- [ ] (Opsional) Monitoring: `compose-up.sh -f deployments/docker-compose.monitoring.yml up -d`
- [ ] (Opsional) Backup cron: `backup-mysql.sh` tiap malam
- [ ] (Opsional) Replica + `DB_REPLICA_ENABLED=true`
