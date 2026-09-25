# Panduan Menyambungkan Perangkat GPS ke Sistem Adatrack

Dokumen ini memandu Anda bagaimana cara menghubungkan perangkat GPS *hardware* (seperti Teltonika, Concox/GT06, dsb.) ke sistem Adatrack Anda sehingga data posisinya bisa terpantau di dasbor.

## Konsep Dasar Koneksi GPS
Setiap perangkat GPS berkomunikasi menggunakan jaringan seluler (via kartu SIM) dengan mengirimkan paket data TCP/UDP ke **IP Server** dan **Port** tertentu. Sistem Adatrack (`ingestion-tcp`) telah siap "mendengarkan" koneksi masuk di berbagai port sesuai dengan bahasa/protokol perangkat yang Anda miliki.

---

## Langkah 1: Persiapan di Dashboard Adatrack
Sebelum menyalakan perangkat GPS, Anda wajib mendaftarkannya terlebih dahulu di sistem agar data yang masuk tidak ditolak (dianggap tidak valid/anonim).

1. Login ke **Adatrack Dashboard**.
2. Masuk ke modul **Manajemen Kendaraan / Aset**.
3. Tambahkan Kendaraan Baru.
4. **Sangat Penting:** Saat mengisi formulir kendaraan, pastikan Anda memasukkan nomor **IMEI** GPS (15 digit angka yang ada di stiker belakang perangkat GPS) secara benar di kolom ID Tracker / IMEI.

---

## Langkah 2: Mengetahui Port Protokol
Perangkat GPS dari pabrikan berbeda menggunakan port yang berbeda pula pada server Adatrack. Berikut adalah daftar port *default* untuk beberapa protokol (pastikan mencocokkan dengan `.env` server Anda):

- **GT06 / Concox** (seperti seri Wetrack, TR, ET): `Port 5023`
- **Teltonika** (FMB, FMC series): `Port 5027`
- **Coban** (TK103, TK303, dsb): `Port 5013`
- **Meitrack**: `Port 5020`
- **Suntech**: `Port 5017`
- **Navigil**: `Port 5012`
- **Castel**: `Port 5019`

*Catatan: Pastikan administrator server/VPS telah membuka (Allow/Open) port-port di atas pada pengaturan Firewall/UFW di server Coolify Anda.*

---

## Langkah 3: Konfigurasi Perangkat GPS (Via SMS atau Kabel)
Untuk memberi tahu GPS ke mana harus mengirim data, Anda harus melakukan *setting* IP dan Port ke dalam perangkat GPS tersebut. Anda bisa menggunakan kabel konfigurasi pabrik (via laptop) atau mengirim pesan **SMS perintah (Command)** ke nomor SIM yang terpasang di GPS.

### Contoh 1: Setting GPS Teltonika (FMB Series)
Umumnya diseting menggunakan perangkat lunak "Teltonika Configurator" di Windows menggunakan kabel USB:
1. Buka Teltonika Configurator, pilih tab **GPRS**.
2. **APN Name**: Isi dengan APN operator seluler kartu SIM (misal: `telkomsel`, `indosatgprs`).
3. **Domain / Server**: Masukkan Alamat IP Server VPS Coolify Anda (contoh: `103.120.30.5`).
4. **Port**: Masukkan `5027`.
5. **Protocol**: Pilih `TCP`.
6. Simpan konfigurasi ke perangkat (Save to Device).

### Contoh 2: Setting GPS Concox/GT06 (Via SMS)
Kirim SMS dari nomor HP Anda ke nomor kartu SIM di dalam GPS:
1. **Atur APN**: `APN,123456,telkomsel#`
2. **Atur IP dan Port Server**: `SERVER,1,103.120.30.5,5023,0#` 
   *(Ganti `103.120.30.5` dengan IP publik VPS Anda).*
3. Tunggu balasan SMS `OK` dari GPS.

*(Catatan: Format SMS mungkin sedikit berbeda tergantung versi firmware pabrikan. Periksa buku manual alat Anda).*

---

## Langkah 4: Verifikasi Data
1. Pastikan kendaraan/GPS berada di ruang terbuka agar dapat menangkap sinyal satelit.
2. Perhatikan lampu indikator pada fisik perangkat GPS (biasanya lampu GSM akan stabil dan lampu GPS berkedip cepat jika terhubung, sesuai petunjuk manual merek tersebut).
3. Buka **Adatrack Dashboard** (Live Tracking / Peta).
4. Periksa kendaraan yang baru ditambahkan. Jika konfigurasi tepat, statusnya akan berubah menjadi **Online** dalam kurun waktu 1 hingga 5 menit dan posisinya akan terlihat di peta.
5. Anda juga bisa mengecek log di server Coolify (Logs untuk service `ingestion-tcp`) untuk melihat apakah *raw hex payload* dari perangkat tersebut sudah masuk.
