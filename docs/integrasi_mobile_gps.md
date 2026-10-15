# Spesifikasi Integrasi & Kontrak API: Pelacakan GPS Smartphone (Mobile Ingestion)

Dokumen ini adalah panduan teknis resmi (*API Contract & Integration Guide*) untuk **Tim Pengembang Aplikasi Mobile (Android & iOS)** dalam mengintegrasikan pelacakan lokasi berbasis smartphone ke dalam platform **Adatrack GPS Platform**.

---

## 1. Arsitektur & Alur Kerja

Platform Adatrack menggunakan arsitektur *event-driven pipeline* berkinerja tinggi. Koordinat GPS yang dikirim oleh aplikasi smartphone akan langsung mengalir melalui pipeline berikut:

```
[Aplikasi Smartphone]
        │
        ▼ (HTTP POST /api/v1/tracking/mobile)
[Backend API Adatrack]
        │
        ▼ (Publikasi Event ke NATS JetStream)
[NATS: telemetry.raw.<device_id>]
        ├──► [Worker Live] ──────► [WebSocket] ──► Dashboard Web (Marker Realtime)
        ├──► [Worker Alert] ─────► Evaluasi Geofence, Overspeed & Alarm SOS
        └──► [Worker Persistence] ► PostgreSQL (Partisi Bulanan th_telemetry_logs)
```

---

## 2. Spesifikasi Endpoint Ingestion

* **Base URL Staging:** `http://api.103.69.106.62.nip.io`
* **Base URL Production:** `https://api.adatrack.com` *(menyesuaikan domain produksi)*
* **Endpoint:** `/api/v1/tracking/mobile`
* **Method:** `POST`
* **Content-Type:** `application/json`

### Header Autentikasi
Aplikasi mobile wajib menyertakan salah satu header autentikasi berikut:
```http
X-Device-Token: <SECRET_DEVICE_TOKEN>
```
*atau*
```http
Authorization: Bearer <JWT_TOKEN_DRIVER>
```

---

## 3. Kontrak Data (JSON Payload)

Aplikasi mobile didukung untuk mengirim data dalam **2 mode**:

### Mode A: Single Location Ping (Kondisi Online Real-Time)
Digunakan saat smartphone memiliki koneksi internet aktif. Direkomendasikan dikirim setiap **10 hingga 15 detik** saat kendaraan/driver bergerak.

```json
{
  "device_id": "APP-DRV-08123456789",
  "lat": -6.2087634,
  "lon": 106.8455990,
  "speed": 42.5,
  "heading": 180.0,
  "altitude": 18.0,
  "accuracy": 8.5,
  "battery_level": 85,
  "is_charging": false,
  "sos": false,
  "timestamp": 1729050000
}
```

### Mode B: Batch Location Sync (Setelah Melewati Area Tanpa Sinyal)
Jika smartphone sempat melewati area *blank spot* (basement, terowongan, daerah pelosok), aplikasi mobile wajib menyimpan titik-titik koordinat ke penyimpanan lokal HP (SQLite / Room / CoreData). Begitu koneksi internet pulih, kirimkan seluruh titik yang tertunda dalam satu request *batch*:

```json
{
  "device_id": "APP-DRV-08123456789",
  "locations": [
    {
      "lat": -6.2087634,
      "lon": 106.8455990,
      "speed": 40.0,
      "heading": 180.0,
      "altitude": 18.0,
      "accuracy": 9.0,
      "battery_level": 86,
      "is_charging": false,
      "sos": false,
      "timestamp": 1729049970
    },
    {
      "lat": -6.2091200,
      "lon": 106.8456100,
      "speed": 45.0,
      "heading": 182.0,
      "altitude": 18.0,
      "accuracy": 8.0,
      "battery_level": 85,
      "is_charging": false,
      "sos": false,
      "timestamp": 1729049985
    }
  ]
}
```

---

## 4. Definisi Setiap Parameter

| Field | Tipe Data | Wajib? | Satuan / Format | Keterangan |
| :--- | :--- | :--- | :--- | :--- |
| `device_id` | `string` | **Ya** | Alfanumerik (max 50) | ID unik perangkat, misal nomor telepon driver (`APP-0812345678`) atau UUID perangkat. |
| `lat` | `float` | **Ya** | Desimal derajat | Lintang GPS WGS84 (contoh: `-6.2087634`). |
| `lon` | `float` | **Ya** | Desimal derajat | Bujur GPS WGS84 (contoh: `106.8455990`). |
| `timestamp` | `integer` | **Ya** | Unix Epoch (Detik/Milidetik) | Waktu pengambilan titik GPS di perangkat (UTC). |
| `speed` | `float` | Opsional | km/jam (km/h) | Kecepatan gerak kendaraan/HP. Default `0.0`. |
| `heading` | `float` | Opsional | Derajat (`0.0 - 360.0`) | Arah kompas gerak (*bearing*). `0` = Utara, `90` = Timur, `180` = Selatan. |
| `altitude` | `float` | Opsional | Meter (m) | Ketinggian di atas permukaan laut. |
| `accuracy` | `float` | Opsional | Meter (m) | Radius akurasi sinyal GPS dari sensor HP. |
| `battery_level`| `integer` | Opsional | Persen (`0 - 100`) | Sisa kapasitas baterai smartphone. |
| `is_charging` | `boolean` | Opsional | `true` / `false` | Menandakan HP sedang dicas (dapat dianalogikan sebagai status kontak ACC ON). |
| `sos` | `boolean` | Opsional | `true` / `false` | `true` jika tombol darurat ditekan oleh driver di layar aplikasi. |

---

## 5. Respons API (*API Responses*)

### Respons Berhasil (200 OK / 202 Accepted)
```json
{
  "success": true,
  "message": "Telemetry accepted",
  "processed_points": 1,
  "server_time": 1729050001
}
```

### Respons Gagal
* **400 Bad Request:** Format JSON salah, `lat`/`lon` tidak valid, atau `device_id` kosong.
  ```json
  {
    "success": false,
    "error_code": "INVALID_PAYLOAD",
    "message": "Field 'device_id', 'lat', and 'lon' are required"
  }
  ```
* **401 Unauthorized:** Token perangkat salah atau tidak disertakan di header.
  ```json
  {
    "success": false,
    "error_code": "UNAUTHORIZED",
    "message": "Invalid or expired device token"
  }
  ```
* **404 Not Found:** `device_id` belum didaftarkan di sistem Adatrack.
  ```json
  {
    "success": false,
    "error_code": "DEVICE_NOT_REGISTERED",
    "message": "Device ID APP-DRV-08123456789 is not mapped to any vehicle or company"
  }
  ```

---

## 6. Pendaftaran Perangkat Smartphone di Web Admin Adatrack

Sebelum aplikasi smartphone mulai mengirim koordinat, Admin tenant atau tim operasional mendaftarkan perangkat terlebih dahulu melalui Dashboard Web Adatrack:
1. Buka menu **Master Devices / Perangkat GPS**.
2. Masukkan ID Perangkat (misal: `APP-DRV-08123456789`).
3. Pilih Protokol: **`SMARTPHONE`** (atau `HTTP_MOBILE`).
4. Tautkan perangkat ini ke **Nama Kendaraan** (misal: `Motor Kurir Budi - B 1234 XYZ`) atau **Profil Driver**.

---

## 7. Praktik Terbaik Pengembang Aplikasi Mobile (*Best Practices*)

1. **Efisiensi Baterai Smartphone (Smart Interval):**
   * **Saat Kendaraan Bergerak:** Ambil koordinat GPS setiap 5–10 detik, kirim ke server setiap 10–15 detik.
   * **Saat Kendaraan Diam (Parkir):** Kurangi interval ke 60–120 detik untuk menghemat baterai HP driver.
2. **Izin Lokasi Latar Belakang (*Background Location Permission*):**
   * **Android:** Wajib menggunakan *Foreground Service* dengan *Sticky Notification* (agar aplikasi tidak dimatikan paksa oleh manajemen baterai sistem Android / MIUI / OneUI).
   * **iOS:** Memerlukan izin `Always Allow` dan pengaturan `UIBackgroundModes: location`.
3. **Filter Akurasi GPS (*Drift Filter*):**
   * Jangan kirim titik koordinat jika nilai `accuracy > 30` meter (biasanya terjadi ketika driver baru keluar dari dalam gedung).

---

## 8. Contoh Implementasi Kode untuk Tim Mobile

### A. Contoh Request cURL (Untuk Uji Coba Postman / Terminal)
```bash
curl -X POST "http://api.103.69.106.62.nip.io/api/v1/tracking/mobile" \
  -H "Content-Type: application/json" \
  -H "X-Device-Token: adatrack-mobile-secret-2026" \
  -d '{
    "device_id": "APP-DRV-08123456789",
    "lat": -6.2087634,
    "lon": 106.8455990,
    "speed": 35.0,
    "heading": 90.0,
    "altitude": 20.0,
    "accuracy": 5.0,
    "battery_level": 90,
    "is_charging": true,
    "sos": false,
    "timestamp": 1729050000
  }'
```

### B. Contoh Implementasi di Flutter / Dart
```dart
import 'dart:convert';
import 'package:http/http.dart' as http;

Future<void> sendLocationToAdatrack({
  required String deviceId,
  required double latitude,
  required double longitude,
  required double speed,
  required double heading,
  required int batteryLevel,
  bool isCharging = false,
  bool isSos = false,
}) async {
  final url = Uri.parse('http://api.103.69.106.62.nip.io/api/v1/tracking/mobile');
  
  final payload = {
    'device_id': deviceId,
    'lat': latitude,
    'lon': longitude,
    'speed': speed,
    'heading': heading,
    'battery_level': batteryLevel,
    'is_charging': isCharging,
    'sos': isSos,
    'timestamp': DateTime.now().toUtc().millisecondsSinceEpoch ~/ 1000,
  };

  try {
    final response = await http.post(
      url,
      headers: {
        'Content-Type': 'application/json',
        'X-Device-Token': 'adatrack-mobile-secret-2026',
      },
      body: jsonEncode(payload),
    );

    if (response.statusCode == 200 || response.statusCode == 202) {
      // Sukses terkirim ke server Adatrack
    } else {
      // Simpan sementara ke database lokal SQLite HP (untuk di-sync nanti)
    }
  } catch (e) {
    // Terjadi masalah koneksi / offline -> Simpan ke database lokal HP
  }
}
```

---

## 9. Kontak Dukungan Backend
Jika tim aplikasi mobile membutuhkan pengujian sandbox atau simulasi kredensial uji coba, silakan hubungi tim Backend Adatrack.
