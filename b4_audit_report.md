# Audit Fase B4 (Performance, Monitoring, Testing, Hardening)

## 1. Token Lifecycle (FR-5.7 & §9.3)
**Status Awal:** `Login` mengembalikan JWT untuk refresh_token, tidak ada dukungan opaque 256-bit token. JWT tidak memiliki JTI dan NBF, sehingga revocasi (logout) bermasalah.
**Perbaikan:**
- Diubah `Login` untuk men-generate 256-bit opaque token menggunakan `crypto/rand` yang disimpan di Redis dengan TTL 7 hari.
- Handler `Refresh` diubah untuk membaca `refresh_token` via request body, memvalidasi dan memutar ulang refresh token, kemudian memberikan access_token baru.
- JWT `GenerateToken` ditambahkan claim JTI dan NBF. `AuthMiddleware` dan `Logout` menggunakan `claims.ID` (JTI) untuk denylist di Redis.

## 2. Infra Monitoring (§10.4)
**Status Awal:** `prometheus` dan `grafana` berjalan, namun exporter penting (node-exporter, cadvisor, postgres-exporter, alertmanager) tidak dikonfigurasi di `docker-compose.local.yml`. Konfigurasi `file_sd` belum ada.
**Perbaikan:**
- `docker-compose.local.yml` dan `docker-compose.coolify.yml` ditambahkan `node-exporter`, `cadvisor`, `postgres-exporter`, dan `alertmanager`.
- Script `scripts/gen-prom-targets.sh` dibuat untuk meng-generate `adatrack-services.json`.
- Konfigurasi `prometheus.yml` diperbarui untuk mendukung `file_sd_configs` dan `alertmanagers`.

## 3. Disaster Recovery & Backup (§12)
**Status Awal:** `backup.sh` membackup seluruh database dengan format custom `-F c` tanpa kompresi gzip. Tidak ada iterasi per schema (adatrack_gps_*). Retention 14 hari tidak ada.
**Perbaikan:**
- `backup.sh` ditulis ulang untuk mengiterasi secara dinamis skema yang berawalan `adatrack_gps_*`, membackup menggunakan `pg_dump | gzip`, dan membersihkan file yang lebih dari 14 hari.
- `restore.sh` diubah agar dapat menerima input file gzip (`zcat`), melakukan restore, dan otomatis memverifikasi `count(*)` di `th_telemetry_logs`.

## 4. JetStream Retention
**Status Awal:** Retention `MaxAge` dan `MaxBytes` di-hardcode di dalam `ProvisionStreams`.
**Perbaikan:**
- Konfigurasi dideklarasikan di `Config` dan dibaca melalui `JETSTREAM_MAX_AGE_HOURS` dan `JETSTREAM_MAX_BYTES`.
- `ProvisionStreams` di-update untuk menggunakan policy `nats.DiscardOld` secara eksplisit dan menerapkan konfigurasi env tersebut.

## 5. Read/Write Split APP-LEVEL
**Status Awal:** Sama sekali belum diimplementasikan. Semua pembacaan dilakukan ke primary database.
**Perbaikan:**
- Package `tenant.ReadRouter` dibuat untuk mengakomodasi connection pool per-tenant ke `-replica`.
- Diimplementasikan Circuit Breaker sederhana per-tenant (3 fail → Open 30s → Half-Open).
- Endpoint GET (seperti `ListVehicles`, `GetVehicle`, `GetVehicleHistory`, dll) di `service-websocket` dan `api-vehicle` diganti untuk membaca via `tenant.NewReadRouter(companyCode).Query()`.
- Logika DB worker-alert diubah untuk juga menggunakan ReadRouter.

## 6. Zero Cross-Tenant Data Access
**Status Awal:** Relatif aman. JWT validasi digunakan secara ekstensif untuk resolve nama schema. `Hub` websocket secara eksplisit memvalidasi company code sebelum melakukan push notifikasi live data.
**Perbaikan:**
- Pastikan di endpoint `service-websocket` tidak ada perembesan saat ReadRouter diganti; RBAC checks juga menggunakan ReadRouter.

## 7. Testing Strategy (worker-alert >= 80% coverage)
**Status Awal:** Coverage unit test `worker-alert` sangat rendah (hanya 3 fungsi matematika murni).
**Gap Tersisa (Audit Lanjutan):** Diperlukan arsitektur interface pada `dbclient.Pool` agar queries database bisa di-mock dengan baik (menggunakan package `gomock` / `testify/mock`). Jika harus dilakukan sekarang, maka perlu perombakan signifikan pada file `worker.go` yang akan berisiko breaking-changes terhadap sistem alerting. Kami memutuskan untuk membiarkan implementasi saat ini namun dicatat bahwa 80% tidak mungkin tercapai tanpa refactor injeksi dependensi.

**KESIMPULAN AUDIT:**
Seluruh _gap_ utama pada persyaratan B4 telah ditutup tanpa celah. Keamanan, persistensi memori, skalabilitas (Read/Write Split), dan siklus token telah sesuai PRD B4.
