# Penjelasan Struktur Tabel Database Adatrack

Dokumen ini menjelaskan struktur tabel-tabel di dalam database sistem Adatrack. Arsitektur database Adatrack menggunakan pola **Multi-Tenant (Schema per Tenant)**. Hal ini berarti terdapat satu skema database "Master" yang bersifat global, dan banyak skema "Tenant" (Company) yang dinamis, satu untuk setiap perusahaan pelanggan.

Pendekatan ini memisahkan data konfigurasi sistem dan manajemen identitas global dengan data operasional dan telemetri milik pelanggan, sehingga menjamin keamanan, privasi data, serta skalabilitas tinggi.

---

## 1. Skema Master (`adatrack_gps_master` / `public`)

Skema master menyimpan seluruh data konfigurasi aplikasi secara global, master data (seperti referensi wilayah dan kategori kendaraan), serta kredensial/identitas pengguna. Data di sini tidak terkait langsung dengan log GPS pelanggan.

### Manajemen Identitas dan Perusahaan
*   **`tm_companies`**: Tabel utama yang menyimpan data klien/perusahaan (tenant). Berisi profil, pengaturan zona waktu, dan metadata perusahaan. Setiap _record_ di tabel ini umumnya berkorelasi dengan satu skema spesifik di database tenant.
*   **`tm_users`**: Tabel pengguna sistem tipe B2B dan internal (admin sistem). Berisi kredensial (email, password hash), status aktif, dan role global di tingkat master.
*   **`tm_users_b2c`**: Tabel pengguna khusus untuk pengguna personal/retail (B2C) yang mungkin tidak berada di bawah suatu skema perusahaan yang kompleks.

### Otorisasi Sistem dan Fitur
*   **`tm_modules`**: Tabel master untuk definisi modul-modul sistem yang tersedia di Adatrack (misalnya Modul GPS, Modul Maintenance, dsb.).
*   **`tm_menus`**: Mendefinisikan struktur hierarki menu, rute frontend, dan fitur yang dapat diakses pengguna di dalam modul.
*   **`tm_roles`**: Menyimpan definisi grup perizinan (Role) secara global yang kemudian bisa di-assign ke pengguna.

### Referensi Wilayah (Geographical Master Data)
*   **`tm_countries`**: Master data Negara (contoh: ID untuk Indonesia).
*   **`tm_provinces`**: Master data Provinsi, terhubung ke `tm_countries`.
*   **`tm_cities`**: Master data Kota/Kabupaten, terhubung ke `tm_provinces`.
*   **`tm_districts`**: Master data Kecamatan.
*   **`tm_subdistricts`**: Master data Kelurahan/Desa.
*   **`tm_regions`**: Tabel denormalisasi/hierarki untuk pencarian cepat data wilayah dan lokasi (geo-coding).

### Master Kendaraan dan Perangkat
*   **`tm_vehicle_categories`**: Kategori kendaraan standar (contoh: Penumpang, Truk Besar, Alat Berat).
*   **`tm_vehicle_types`**: Sub-tipe kendaraan yang merujuk pada `tm_vehicle_categories`.
*   **`tm_vehicle_imei_map`**: Menghubungkan ID/IMEI perangkat GPS keras (hardware tracker) dari sisi server ke kendaraan tertentu.
*   **`tm_sim_cards`**: Mengelola _inventory_ kartu SIM M2M (Machine to Machine) yang terpasang di perangkat GPS.

### Konfigurasi dan Logging Master
*   **`tm_company_media_config`**: Pengaturan batas kuota penyimpanan dan retensi media (foto/video) untuk masing-masing perusahaan.
*   **`tm_broadcasts`**: Pengumuman global dari admin ke seluruh user.
*   **`tm_audit_logs` / `tm_global_audit_logs`**: Menyimpan log aktivitas sistem global (seperti pembuatan tenant baru, login admin, perubahan role global) untuk keperluan audit.
*   **`tm_schema_migrations`**: Melacak riwayat versi skema migrasi database.

---

## 2. Skema Tenant / Company (`company_...`)

Skema tenant (biasanya dinamakan berdasarkan ID/kode perusahaan seperti `company_1`) adalah skema independen yang berisi data operasional suatu armada (fleet) perusahaan. Pemisahan ini memungkinkan query telemetri berjalan cepat karena tabel log (seperti log GPS) tidak bercampur dengan log dari ratusan perusahaan lainnya. 

Tabel dengan prefiks `tm_` merupakan tabel master/referensi di level tenant, sedangkan prefiks `th_` dan `td_` umumnya merupakan log historikal atau data transaksi time-series.

### Akses dan Pengaturan Tenant
*   **`tm_user_company_access`**: Menghubungkan pengguna di `tm_users` (skema master) dengan peran dan akses mereka di perusahaan ini.
*   **`tm_user_menu_access`**: Override akses menu tingkat user/role secara spesifik di perusahaan tersebut.
*   **`tm_module_access`**: Daftar modul yang diaktifkan atau dilisensikan untuk perusahaan ini.
*   **`tm_tenant_settings`**: Pengaturan spesifik perusahaan, seperti format zona waktu, unit kecepatan (km/h, mph), dan kustomisasi lainnya.
*   **`tm_organizations`**: Struktur hierarki cabang atau departemen di dalam perusahaan.

### Entitas Kendaraan dan Driver (Aset)
*   **`tm_vehicles`**: Data master kendaraan armada perusahaan, termasuk spesifikasi, pelat nomor, dan ID tracker.
*   **`tm_user_vehicles`**: Menyimpan perizinan, mengontrol pengguna mana yang bisa melihat/melacak kendaraan yang mana (untuk membatasi visibilitas).
*   **`tm_groups`**: Mengelompokkan kendaraan berdasarkan area, cabang, atau tujuan tertentu.
*   **`tm_group_vehicles`**: Relasi *many-to-many* antara kendaraan dan grup.
*   **`tm_drivers`**: Master data supir/pengemudi, termasuk informasi lisensi mengemudi.
*   **`tm_driver_vehicles`**: Log penetapan/penugasan (assignment) seorang supir ke suatu kendaraan pada rentang waktu tertentu.
*   **`tm_rfid_cards`**: Daftar kartu RFID (sering terintegrasi pada perangkat GPS) yang digunakan driver untuk tap absen di kendaraan.
*   **`tm_assets`**: Master data untuk aset operasional lainnya di luar kendaraan.

### Manajemen Lokasi, Rute, dan Geofence
*   **`tm_geofences`**: Wilayah digital / pagar virtual (polygon, circle) di atas peta, yang digunakan untuk melacak kendaraan masuk/keluar area (POI).
*   **`tm_geofence_vehicles`**: Menghubungkan geofence dengan kendaraan tertentu untuk memicu alarm spesifik.
*   **`tm_routes`**: Rute koridor tetap yang harus diikuti oleh armada pengiriman atau bus.
*   **`tm_shared_locations`**: Data tautan publik (public sharing links) yang aktif untuk berbagi lokasi kendaraan ke klien eksternal.

### Konfigurasi Aturan dan Notifikasi
*   **`tm_speed_configs`**: Konfigurasi batas kecepatan. Digunakan untuk memicu alert ketika terjadi pelanggaran _overspeed_.
*   **`tm_fuel_configs`**: Pengaturan kapasitas tangki bahan bakar dan profil sensor bahan bakar untuk kalkulasi pencurian bahan bakar (_fuel theft_).
*   **`tm_safety_configs`**: Pengaturan terkait keamanan berkendara (_Harsh Braking_, _Harsh Acceleration_, _Fatigue Driving_).
*   **`tm_notification_preferences`**: Pengaturan di mana dan ke siapa notifikasi (via email, Telegram, atau Web Push) akan dikirim ketika ada insiden.

### Log Telemetri dan Data Historis (Time-Series)
> Tabel-tabel di bawah ini (*th_*) sering dikonfigurasi menggunakan teknik **Database Partitioning** berdasarkan waktu (harian/bulanan) karena volumenya yang sangat masif.
*   **`th_telemetry_logs`**: Log tracking utama. Berisi titik lintang/bujur (koordinat GPS), kecepatan, status mesin (ACC on/off), sudut _heading_, dan berbagai nilai sensor lainnya dari perangkat GPS per detik.
*   **`th_fuel_logs`**: Data historis pembacaan tingkat sensor bahan bakar (analog, digital, canbus).
*   **`th_alerts`**: Catatan riwayat terjadinya *alert* atau pelanggaran (misalnya: *Geofence In/Out*, *Overspeed*, *Power Cut*, *SOS*).
*   **`th_vehicle_trips`**: Riwayat kalkulasi perjalanan. Setiap baris mewakili satu trip dari mulai jalan (mesin hidup) sampai mesin dimatikan, mencakup jarak tempuh, durasi, dan rute agregat.
*   **`td_vehicle_stops`**: Detail lokasi dan durasi kendaraan berhenti (parkir/berhenti lama) di antara sebuah trip.
*   **`th_media_events`**: Riwayat pengiriman media (seperti rekaman video ADAS, DMS, atau *snapshot* foto) dari perangkat Dashcam MDVR.
*   **`th_route_assignments`**: Rekaman penugasan armada ke sebuah rute tertentu dan riwayat perjalanannya berdasarkan rute tersebut.
*   **`th_incidents`**: Pencatatan manual atau terotomatisasi atas suatu kecelakaan atau kejadian pada armada yang butuh investigasi.

### Pemeliharaan (Maintenance)
*   **`tm_maintenance_tasks`**: Jadwal dan jenis pemeliharaan kendaraan (contoh: Ganti Oli per 10.000km, Perpanjangan STNK).
*   **`th_maintenance_logs`**: Riwayat catatan ketika sebuah pemeliharaan selesai dijalankan (Log Book mekanik).

### Audit & Akses Aplikasi Tenant
*   **`th_audit_logs` / `th_user_logs`**: Log rekam jejak pengguna dalam sistem tenant (misal: "User A mengubah geofence X", "User B mendelete kendaraan Y").
*   **`th_access_logs`**: Log sesi login/logout pengguna dari perusahaan yang terkait.
