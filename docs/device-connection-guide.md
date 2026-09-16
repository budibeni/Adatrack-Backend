# Panduan Mengkoneksikan Device GPS ke Aplikasi AJB - GPS

## Pendahuluan

Panduan ini akan menjelaskan langkah demi langkah bagaimana cara memasang dan mengkoneksikan device GPS ke aplikasi sistem pelacatan kendaraan **AJB - GPS**. Device GPS yang didukung adalah device yang menggunakan **protokol GT06/Concox**, seperti:

- **GT06**
- **VG01/VG02/VG03/VG04**
- **EG02/EG03**
- **GT800**
- Dan device lain yang mendukung protokol serupa.

Dengan mengikuti panduan ini, Anda dapat memastikan device GPS Anda terhubung dengan benar ke sistem, dan data telemetri kendaraan dapat diproses serta ditampilkan di aplikasi.

---

## Prasyarat

Sebelum memulai, pastikan Anda telah menyiapkan hal-hal berikut:

1. **Device GPS**:
   - Device GPS Anda sudah aktif dan dalam kondisi normal.
   - Device mendukung konfigurasi melalui SMS atau software konfigurasi (misalnya TrackSolid, GPSGate).

2. **IMEI Device**:
   - IMEI adalah nomor identifikasi unik device. Pastikan IMEI terdaftar di sistem.
   - IMEI biasanya terdiri dari 15 digit angka dan dapat ditemukan di perangkat keras device atau dikirim lewat SMS pertama kali device aktif.

3. **Server (AJB - GPS)**:
   - Server tempat sistem **AJB - GPS** berjalan sudah siap dan terhubung ke jaringan.
   - Service `ingestion-tcp` sudah berjalan dan mendengarkan port protokol sesuai device:
     - **GT06/Concox**: port `9000` (`TCP_PORT`).
     - **Teltonika (Codec 8/8E)**: port `9001` (`TELTONIKA_TCP_PORT`).
     - **TK103 / GT-clone**: port `9002` (`TK103_TCP_PORT`).
   - Atur IP/port di konfigurasi device mengikuti tabel di atas.

4. **Firewall & Port Server**:
   - Pastikan port protokol device Anda (9000/9001/9002 atau port yang dikonfigurasi) sudah
     terbuka di server agar device dapat mengirimkan data.

## Langkah 1: Konfigurasi Device GPS

Setelah semua prasyarat terpenuhi, langkah pertama adalah mengonfigurasi device GPS agar mengirimkan data telemetry ke server AJB - GPS. Berikut adalah langkah-langkahnya:

### 1. Hubungkan Device ke Komputer atau SMS Gateway

- Jika menggunakan software konfigurasi (misalnya TrackSolid, GPSGate):
  - Colokkan device GPS ke komputer melalui kabel USB-to-Serial atau Bluetooth (jika tersedia).
  - Buka software konfigurasi yang sesuai.
- Jika menggunakan SMS:
  - Pastikan Anda memiliki SIM card yang tersambung ke device, dan nomor telepon device yang valid.

### 2. Atur Alamat IP dan Port Server

- Di software konfigurasi, carilah menu pengaturan server (biasanya bernama "Server", "APN", atau "Network").
- Atur nilai-nilai berikut:
  - **Server IP atau Domain**: `[GANTI_DENGAN_IP_SERVER_ANDA]`
    *(Contoh: `192.168.1.100` atau `yourserver.domain.com`)*
  - **Port**: `9000`
    *(Port default `ingestion-tcp`. Jika Anda mengganti port ini di konfigurasi sistem, gunakan port yang sesuai.)*
- **Save & Send**:
  - Klik tombol "Save" atau "Send" di software untuk menyimpan pengaturan ke device.
  - Jika menggunakan SMS, kirim perintah SMS berikut (format bisa bervariasi tergantung device):
    ```
    SERVER,[GANTI_DENGAN_IP_SERVER_ANDA],9000
    ```

### 3. Pastikan Device Terhubung

Setelah mengirimkan konfigurasi, device GPS akan mencoba untuk terhubung ke server Anda. Pastikan:

- Device memiliki sinyal GSM yang baik.
## Langkah 2: Pendaftaran IMEI Device di Sistem

Agar sistem dapat mengenali dan memproses data dari device Anda, IMEI device harus terdaftar. Berikut adalah cara memverifikasi atau mendaftarkannya:

### 1. Dapatkan IMEI Device

- IMEI biasanya dapat ditemukan:
  - Di label fisik device GPS.
  - Dengan mengirim SMS dengan isi:
    ```
    IMEI
    ```
  - Atau, IMEI akan muncul otomatis di log sistem `ingestion-tcp` saat pertama kali device mengirimkan data.

### 2. Verifikasi di Database (Opsional)

- Buka koneksi ke database PostgreSQL sistem:
  ```bash
  psql -U adatrack_gps_user -d adatrack_gps_db
  ```
- Cek apakah IMEI sudah ada:
  ```sql
  SELECT * FROM vehicles WHERE imei = 'NOMOR_IMEI_DEVICE_ANDA';
  ```
- Jika tidak ada, daftarkan IMEI baru (opsional):
  ```sql
  INSERT INTO vehicles (imei, name, status) 
  VALUES ('[IMEI_DEVICE]', '[NAMA_KENDARAAN]', 'active');
  ```
  > Ganti `[IMEI_DEVICE]` dan `[NAMA_KENDARAAN]` dengan nilai sebenarnya.

## Langkah 3: Verifikasi Koneksi Device

Setelah device dikonfigurasi dan IMEI terdaftar, langkah selanjutnya adalah memverifikasi apakah device sudah terhubung dan mengirimkan data.

### 1. Cek Log Service `ingestion-tcp`

- Buka terminal di server, dan cek log service `ingestion-tcp`:
  ```bash
  docker logs -f backend_ingestion-tcp_1
  ```
  *(atau sesuaikan dengan nama container/service Anda)*

- Jika device sudah terhubung, Anda akan melihat log seperti ini:
  ```
  [INFO] [device] telemetry received IMEI=123456789012345 ...
  ```

#### Menguji dengan frame GT06 yang valid

Frame GT06 memakai **CRC-16 (CRC-ITU)** sebagai *Error Check* (bukan penjumlahan byte), dengan start bit `0x78 0x78`, length 1 byte, dan stop bit `0x0D 0x0A`. Frame **login** untuk IMEI `123456789012345` (protokol `0x01`, serial `0x00 0x01`) adalah:

```
78 78 0D 01 01 23 45 67 89 01 23 45 00 01 8C DD 0D 0A
       --  -- -- -- -- -- -- -- -- -- -- -- --------
       len proto       IMEI (15 digit)     serial  CRC-16
```

Anda bisa mengujinya lewat `nc`:

```bash
printf '\x78\x78\x0d\x01\x01\x23\x45\x67\x89\x01\x23\x45\x00\x01\x8c\xdd\x0d\x0a' | nc -w 5 localhost 9000
```

Sistem hanya memproses IMEI yang terdaftar (anti-spoofing); IMEI tak dikenal ditolak.

### 2. Cek Subject NATS (Opsional untuk Pengembang)

- Jika Anda ingin memastikan data mengalir hingga ke broker pesan:
  ```bash
  nats-sub "telemetry.raw.123456789012345"
  ```
- Data telemetry akan muncul di sini jika device berhasil mengirimkan data.

### 3. Cek Status Koneksi di Redis (Opsional)

- Device yang sudah terhubung akan memiliki status “online” dan data live di Redis:
  ```bash
  redis-cli
  KEYS vehicle:state:*
  GET vehicle:state:123456789012345
  ```



