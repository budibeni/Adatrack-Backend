# ADATRACK Frontend - Modul, Menu, dan Fitur

Dokumen ini menjelaskan struktur modul, menu navigasi, dan fitur-fitur yang tersedia di aplikasi Frontend ADATRACK, baik untuk aplikasi **Business** maupun **Personal**.

## 1. Aplikasi ADATRACK Business

Aplikasi Business ditujukan untuk kebutuhan *fleet management* skala perusahaan. Menu navigasi dibagi menjadi beberapa grup (modul) utama.

### 1.1 Utama (Main)
- **Beranda (Home)** (`/`): Dashboard utama menampilkan ringkasan operasional.
- **Pemantauan (Tracking)** (`/tracking`): Pemantauan posisi kendaraan (Live Map), Heatmap, dan Playback riwayat perjalanan.
- **Perjalanan (Trips)** (`/trips`): Manajemen dan rute perjalanan kendaraan.

### 1.2 Master Data (Master)
- **Armada (Vehicles)** (`/vehicles`): Manajemen data kendaraan (Master Armada).
- **Pengemudi (Drivers)** (`/drivers`): Manajemen profil pengemudi.
- **Geofence (Geofences)** (`/geofences`): Pengaturan batas wilayah operasional atau area larangan.
- **Grup (Groups)** (`/groups`): Pengelompokan kendaraan atau pengemudi.
- **Rute (Routes)** (`/routes`): Penentuan jalur perjalanan operasional.

### 1.3 Akses (Access)
- **Personel (Personel)** (`/personel`): Manajemen pengguna dan akses.
- **Kartu (Card)** (`/card`): Pengelolaan kartu akses / RFID.
- **Log (Log)** (`/log`): Riwayat akses / akses kontrol.

### 1.4 Aset & Perawatan (Asset)
- **Aset (Assets)** (`/assets`): Manajemen keseluruhan aset bisnis.
- **Perawatan (Maintenance)** (`/maintenance`): Penjadwalan servis, perbaikan, dan manajemen suku cadang.

### 1.5 Keamanan (Safety)
- **Keamanan (Safety)** (`/safety`): Indikator keamanan dan skor mengemudi.
- **Insiden (Incidents)** (`/incidents`): Pelaporan dan analisis pelanggaran/insiden (misal: *overspeed*, rem mendadak).

### 1.6 Analisis & Laporan (Analysis)
- **Laporan (Reports)** (`/reports`): Pembuatan laporan berbasis data pergerakan, status, dan efisiensi.
- **Analitik (Analytics)** (`/analytics`): Dashboard lanjutan dan tren.

### 1.7 Modul Spesifik Industri (Industry Specific Modules)
Selain modul standar, aplikasi Business memiliki modul tambahan sesuai model bisnis perusahaan:

- **Rental**: Manajemen kendaraan sewa (`/rental/customers`, `/rental/vehicles`, `/rental/reservations`, `/rental/contracts`, `/rental/handovers`, `/rental/returns`, `/rental/reports`).
- **Transport**: Manajemen jadwal dan transportasi penumpang (`/transport/dashboard`, `/transport/vehicles`, `/transport/schedules`, `/transport/departures`, `/transport/checker`).
- **Logistics**: Manajemen logistik dan pengiriman barang (`/logistics/dashboard`, `/logistics/customers`, `/logistics/orders`, `/logistics/shipments`, `/logistics/manifests`, `/logistics/deliveries`, `/logistics/pod`, `/logistics/reports`).
- **Sales**: Manajemen aktivitas penjualan dan pergerakan tenaga penjual di lapangan (`/sales/dashboard`, `/sales/customers`, `/sales/visits`, `/sales/prospects`, `/sales/quotes`, `/sales/orders`, `/sales/reports`).
- **Field Service**: Manajemen tugas lapangan dan teknisi (`/field-service/dashboard`, `/field-service/customers`, `/field-service/work-orders`, `/field-service/assignments`, `/field-service/schedules`, `/field-service/technicians`, `/field-service/completions`, `/field-service/reports`).
- **Patrol**: Manajemen patroli keamanan (`/patrol/dashboard`, `/patrol/schedules`, `/patrol/assignments`, `/patrol/checkpoints`, `/patrol/inspections`, `/patrol/incidents`, `/patrol/reports`, `/patrol/history`).
- **Project Site**: Manajemen proyek di lokasi kerja tertentu (`/project/dashboard`, `/project/projects`, `/project/sites`, `/project/assignments`, `/project/schedules`, `/project/activities`, `/project/incidents`, `/project/reports`).

### 1.8 Administrasi (Administration)
- **Akses Pengguna (Users Access)** (`/users`): Pengaturan *Role-Based Access Control* (RBAC).
- **Organisasi (Organization)** (`/organization`): Pengaturan struktur hierarki perusahaan.
- **Perangkat GPS (GPS Devices)** (`/gps-devices`): Manajemen dan inventarisasi *hardware* GPS tracker.
- **Integrasi (Integrations)** (`/integrations`): Pengaturan API dan Webhook eksternal.
- **Pengaturan (Settings)** (`/settings`): Pengaturan dasar sistem dan preferensi pengguna.

---

## 2. Aplikasi ADATRACK Personal

Aplikasi Personal difokuskan untuk pengguna individu yang membutuhkan *tracking* kendaraan atau aset pribadi secara praktis.

### Modul & Menu Utama
- **Pemantauan (Tracking)** (`/`): Peta langsung untuk melacak lokasi terkini dari aset pribadi (mobil, motor, dsb).
- **Statistik (Statistics)** (`/statistics`): Ringkasan aktivitas dan metrik penggunaan aset.
- **Pengaturan (Settings)** (`/settings`): Konfigurasi aplikasi, bahasa (Indonesia/English), tema (Light/Dark), serta pengaturan peringatan/notifikasi dasar.

---

## 3. Fitur Lintas Aplikasi (Cross-Cutting Features)
Aplikasi Business dan Personal juga menggunakan fitur *foundation* dan *shared* (tersedia di `packages/`):
- **UI & Design System**: Komponen *Tailwind* seragam yang *accessible* dan *responsive*.
- **Maps Foundation**: Layanan peta dasar.
- **Internationalization (i18n)**: Dukungan bahasa ganda (Indonesia & English).
- **Theming**: Dukungan Dark Mode & Light Mode.
- **Notifikasi & Berbagi Lokasi**: Kemampuan pengelolaan peringatan dan membagikan lokasi sementara ke publik.

