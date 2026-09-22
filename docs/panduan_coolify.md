# Panduan Deployment Menggunakan Coolify

Dokumen ini menjelaskan langkah-langkah untuk melakukan deployment aplikasi backend Adatrack ke server menggunakan **Coolify**.

## 1. Persiapan Server & Coolify
1. Pastikan Anda memiliki VPS/Server (misalnya di DigitalOcean, AWS, atau provider lain) dengan spesifikasi minimal yang direkomendasikan (RAM 4GB+, CPU 2 Core+).
2. Install Coolify di server Anda dengan menjalankan skrip instalasi resmi dari dokumentasi Coolify.
3. Setelah terinstal, akses dashboard Coolify melalui IP server Anda (biasanya di port 8000) dan selesaikan setup awal (membuat akun admin).

## 2. Membuat Project dan Menghubungkan Git
1. Di dalam Dashboard Coolify, buat **Project** dan **Environment** baru (misalnya Project: `Adatrack`, Environment: `Production`).
2. Masuk ke environment tersebut, lalu klik tombol **+ Add Resource**.
3. Pilih sumber kode dari **Git Repository** (GitHub/GitLab).
4. Hubungkan akun GitHub Anda, lalu pilih repositori `budibeni/Adatrack-Backend`.
5. Tentukan branch yang ingin di-deploy, umumnya `main`.

## 3. Konfigurasi Docker Compose
Aplikasi Adatrack backend dirancang untuk di-deploy menggunakan Docker Compose.
1. Setelah repositori terpilih, pada bagian *Build Pack*, pilih **Docker Compose**.
2. Di kolom konfigurasi, tentukan lokasi file compose: arahkan ke `docker-compose.coolify.yml`. (Perhatikan lokasi direktori jika file berada di folder `backend`).
3. Coolify akan secara otomatis membaca struktur layanan (services) dari file tersebut (termasuk *database*, *redis*, *nats*, *monitoring*, dan aplikasi *backend*).

## 4. Pengaturan Environment Variables (.env)
Aplikasi membutuhkan konfigurasi *environment* agar dapat terhubung ke database dan layanan lainnya.
1. Di Dashboard Coolify, buka tab **Environment Variables** untuk resource tersebut.
2. Buka file `backend/.env.coolify` dari repositori Anda.
3. Salin isi dari `.env.coolify` ke dalam fitur *Bulk Edit* di Coolify, atau tambahkan satu per satu.
4. Sesuaikan variabel penting:
   - `DB_PASSWORD`: Ganti dengan password yang kuat.
   - `JWT_SECRET`: Ganti dengan string acak rahasia.
   - Variabel lain sesuai kebutuhan.

## 5. Proses Deployment
1. Setelah *Environment Variables* tersimpan, klik tombol **Deploy**.
2. Coolify akan memulai proses:
   - Mengunduh kode sumber (cloning).
   - Membangun (building) image Docker.
   - Menjalankan container sesuai urutan di file *compose*.
3. Anda dapat memantau log proses deployment secara real-time di tab *Deployment Logs*.
4. Jika deployment sukses, aplikasi backend Anda sudah berjalan.

## 6. Mengatur Domain dan Proxy
1. Pada masing-masing layanan yang memerlukan eksposur ke internet (seperti API utama), Anda bisa mengisi **Domains** pada dashboard Coolify.
2. Pastikan domain sudah diarahkan (A Record DNS) ke IP server Coolify.
3. Untuk koneksi GPS (TCP), Coolify/Docker memungkinkan port di-*binding* langsung tanpa melalui HTTP proxy. Pastikan port GPS (misalnya 5027 untuk Teltonika) telah diekspos (Expose Port) pada pengaturan container dan firewall (UFW) di server VPS Anda telah dibuka.
