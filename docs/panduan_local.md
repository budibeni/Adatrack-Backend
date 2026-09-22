# Panduan Deployment di Local (Development)

Dokumen ini memandu Anda untuk menjalankan aplikasi backend Adatrack di mesin lokal (komputer/laptop) untuk keperluan _development_ atau percobaan.

## 1. Kebutuhan Sistem (Prasyarat)
Sebelum memulai, pastikan perangkat Anda sudah terinstal:
- **Git** (untuk mengambil kode sumber)
- **Go** (Versi 1.22 atau terbaru, untuk kompilasi kode jika tidak menggunakan Docker penuh)
- **Docker** dan **Docker Compose** (wajib untuk menjalankan layanan pendukung seperti database dan broker).
- **Make** (direkomendasikan, untuk menjalankan *Makefile*).

## 2. Kloning Repositori
Buka terminal dan lakukan _clone_ repositori:
```bash
git clone git@github.com:budibeni/Adatrack-Backend.git
cd Adatrack-Backend/backend
```

## 3. Konfigurasi Environment (File .env)
Sistem membutuhkan file konfigurasi lokal.
1. Secara bawaan, sudah terdapat file bernama `.env.local` di folder `backend`.
2. File ini sudah berisi konfigurasi *default* (termasuk *password* lokal) yang cukup untuk menjalankan sistem di komputer. Anda tidak perlu mengubahnya kecuali ada *port* yang bentrok di PC Anda.

## 4. Menjalankan Aplikasi

Terdapat dua cara untuk menjalankan aplikasi di lokal, menggunakan *Make* (direkomendasikan) atau menggunakan *Docker Compose* secara manual.

### Cara 1: Menggunakan Makefile (Direkomendasikan)
Di dalam folder `backend`, cukup ketik perintah:
```bash
make dev
```
Perintah ini akan secara otomatis:
- Mengeksekusi script lokal untuk menjalankan infrastruktur (PostgreSQL, Redis, NATS, Grafana).
- Menjalankan servis API lokal.

Untuk menghentikan dan menghapus semua data (mereset ke awal):
```bash
make down
```

### Cara 2: Menggunakan Docker Compose Langsung
Jika Anda lebih suka menggunakan perintah Docker:
```bash
docker-compose --env-file .env.local -f docker-compose.local.yml up -d --build
```
Perintah di atas akan membangun image Docker dan menjalankan seluruh komponen aplikasi di latar belakang (*background*).
Untuk melihat log aplikasi:
```bash
docker-compose -f docker-compose.local.yml logs -f
```

## 5. Menjalankan Migrasi Database
Jika database dijalankan untuk pertama kali, tabel-tabel mungkin belum terbuat. Jalankan skrip migrasi:
```bash
make migrate-up
```
Perintah ini akan membaca semua file SQL di folder `database/migrations` dan membentuk struktur tabel di Postgres lokal.

## 6. Uji Coba Keberhasilan
- Pastikan API merespons dengan mengakses endpoint indikator _health check_ di peramban (browser) atau menggunakan curl:
  ```bash
  curl http://localhost:8080/healthz
  ```
  Anda harus melihat balasan "OK".
- Sistem siap menerima koneksi GPS pada *port* yang ditentukan di file `.env.local` (misal 5027 untuk Teltonika).
