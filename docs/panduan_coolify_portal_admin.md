# Panduan Deployment Portal Admin ke Coolify

Dokumen ini menjelaskan langkah-langkah untuk mendeploy aplikasi **Portal Admin** (berbasis Next.js) ke server menggunakan Coolify.

## Konsep Dasar
Aplikasi Portal Admin ini dibangun menggunakan kerangka kerja (framework) **Next.js** dan sudah dilengkapi dengan file `Dockerfile` tipe *standalone* yang sangat optimal untuk produksi. Anda tidak perlu menggunakan Docker Compose untuk mendeploy aplikasi ini; kita akan menggunakan fitur *Dockerfile Builder* bawaan Coolify.

---

## Langkah 1: Menambahkan Resource di Coolify
1. Buka Dashboard Coolify dan masuk ke Project & Environment yang sama dengan backend Anda (agar rapi).
2. Klik tombol **+ Add Resource**.
3. Pilih **Git Repository** (Public/Private sesuai pengaturan repositori Anda).
4. Hubungkan akun GitHub Anda, lalu pilih repositori proyek ini (`budibeni/Adatrack-Backend` atau repositori tempat folder `admin` berada).
5. Pilih branch utama (biasanya `main` atau `master`).

---

## Langkah 2: Konfigurasi Build Pack
Karena ini adalah monorepo (atau proyek dengan folder terpisah untuk admin dan backend), Anda harus memberi tahu Coolify di mana lokasi aplikasi admin berada.

1. Setelah resource ditambahkan, buka tab **Configuration**.
2. Pada bagian **Build Pack**, pastikan yang terpilih adalah **Dockerfile** (bukan Nixpacks atau Docker Compose).
3. **Penting:** Ubah pengaturan *Base Directory* (atau *Working Directory*) ke `/admin`.
4. Pastikan lokasi file Docker (Dockerfile location) menunjuk ke `/admin/Dockerfile`.
5. Coolify akan mengatur port internal ke `3000` (sesuai `EXPOSE 3000` di Dockerfile). Biarkan pengaturan port ini secara _default_.

---

## Langkah 3: Mengatur Environment Variables
Aplikasi Next.js mengompilasi variabel berawalan `NEXT_PUBLIC_` **pada saat proses build (Kompilasi)**. Oleh karena itu, variabel ini wajib dimasukkan *sebelum* Anda mengklik Deploy.

1. Pindah ke tab **Environment Variables**.
2. Tambahkan variabel-variabel berikut (sesuaikan URL dengan domain backend Anda yang sebenarnya yang sudah online):
   - `NEXT_PUBLIC_API_URL` = `https://api.domain-anda.com` (URL backend utama)
   - `NEXT_PUBLIC_SERVICE_MONITOR_URL` = `https://monitor.domain-anda.com` (URL untuk service monitor jika dipisah, atau samakan dengan backend)
   - `NEXT_PUBLIC_GRAFANA_URL` = `https://grafana.domain-anda.com` (URL Grafana yang sudah di-deploy)
3. Centang opsi **Build Variable** (atau pastikan variabel ini tersedia saat fase build di Coolify) agar Next.js bisa "membacanya" dan menanamkannya ke dalam file HTML/JS.

---

## Langkah 4: Pengaturan Domain (Reverse Proxy)
1. Di tab **Configuration**, cari bagian **Domains**.
2. Masukkan alamat domain untuk portal admin ini, misalnya `https://admin.domain-anda.com`.
3. Pastikan Anda sudah mengarahkan DNS (A Record) untuk `admin.domain-anda.com` ke IP Publik server Coolify Anda.
4. Coolify otomatis akan membuatkan sertifikat SSL (HTTPS).

---

## Langkah 5: Deployment
1. Setelah semua konfigurasi dan _environment variables_ siap, klik tombol **Deploy**.
2. Proses deployment akan memakan waktu sekitar 1 hingga 3 menit:
   - Coolify akan men-download kode dari Git.
   - Menjalankan `npm ci` untuk mengunduh dependensi Node.js.
   - Menjalankan `npm run build` untuk mengompilasi halaman Next.js.
   - Memulai *container* aplikasi.
3. Anda dapat melihat log secara real-time di tab **Deployments**.
4. Jika proses selesai dan muncul tulisan *Healthy*, silakan buka URL/Domain admin Anda di peramban web (browser).

---

### Troubleshooting Umum
- **Aplikasi berhasil jalan tapi tidak bisa memanggil API (Network Error):** Kemungkinan Anda lupa mengatur variabel `NEXT_PUBLIC_` atau mengaturnya *setelah* proses _deploy_. Solusinya: Ubah *environment variables*, lalu klik **Redeploy** (tanpa cache) agar Next.js di-_build_ ulang dengan URL yang baru.
- **Image Size terlalu besar:** Dockerfile sudah menggunakan mode `standalone` sehingga ukuran akhirnya (image size) cukup kecil (di bawah 150MB). Jika Coolify gagal _build_ karena kehabisan RAM, pertimbangkan menaikkan RAM server (Next.js butuh sekitar 1-2GB RAM saat _build time_).
