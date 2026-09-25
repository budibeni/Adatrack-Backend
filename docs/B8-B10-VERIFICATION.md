# B8 / B9 / B10 — Verifikasi & Audit (2026-09-24)

> Ringkasan bukti nyata untuk fase **B8 (Advanced Fleet Features)**, **B9 (Protocol
> Expansion)** dan **B10 (Normalisasi & Konfigurasi)**. Semua klaim di bawah punya
> perintah + hasil yang bisa diulang; item yang belum lengkap ditulis eksplisit di
> §4 (gap), mengikuti aturan `.agent/01-global-rules.md`.
>
> **Revisi 2 (audit lanjutan 2026-09-24):** lima belas temuan audit sudah diperbaiki —
> ACK Navigil, payload Navigil MSG 8/18, peta identitas Navigil, framing+respons
> Castel, downlink durable JetStream, bug SQL audit command, encoder TK103,
> E2E downlink baru (`scripts/e2e-commands.sh`), ambiguitas balasan device saat dua
> perintah in-flight, replay backlog JetStream, metrik `telemetry_interval_seconds`
> yang selalu 0, **payload posisi Suntech**, **dua perintah TK103 tambahan**, dan
> **skor mengemudi ternormalisasi per jarak**. Rincian: §5.

## 0. Ringkasan

| Fase | Status | Bukti utama |
|---|---|---|
| **B8** Downlink `DYD#` / driver behaviour / maintenance | ✅ selesai (gap tersisa: encoder non-GT06/TK103, atribusi skor ke driver) | registry koneksi + dispatcher `command.request.>` **durable JetStream** + ACK `0x21` → `td_device_commands`; encoder GT06 + **7 perintah TK103**; alert `driver_event`/`maintenance_due`; **skor mengemudi per 100 km**; migrasi `021`–`025`; E2E `e2e-commands.sh` 5/5 |
| **B9** Protocol expansion (9 keluarga) | 🟡 sebagian (8 keluarga ingest penuh, 1 framing + respons) | registry decoder pluggable; 11 listener; **Xexun/Navigil/Suntech end-to-end** → `th_telemetry_logs` + Redis live state; **Castel** framing benar + balasan login/heartbeat; test vector per protokol |
| **B10** Normalisasi & konfigurasi | ✅ selesai | guard prefix `tm_/th_/td_` otomatis; `TELEMETRY_INTERVAL_SECONDS` + metrik gauge; config ganda LOCAL/COOLIFY; `internal/validate` (§8.5/§9.6) |

E2E regresi pipeline setelah refactor ingestion: **5/5 PASS**.
E2E downlink command (angka terakhir): **5/5 PASS**.


## 1. B8 — Advanced Fleet Features

### 1.1 Downlink / remote commands (`DYD#`) — PRD §21.2 row 1

```
api-vehicle  POST /api/v1/vehicles/{id}/commands
             ├─ validate (whitelist + interval 5..86400)
             ├─ INSERT td_device_commands (status=pending)        ← baris audit
             └─ publish command.request.<COMPANY>
ingestion-tcp  (queue group `command`, satu-satunya pemegang socket device)
             ├─ registry IMEI→conn  ──tidak ada──▶ status=offline
             ├─ encode per protokol (GT06 0x80) ──▶ tulis ke socket ▶ status=sent
             ├─ balasan 0x21 "DYD=Success!" ─▶ status=acked (+ack_content)
             ├─ tanpa balasan ≤ COMMAND_ACK_TIMEOUT_SECONDS ─▶ status=timeout
             └─ publish command.result.<COMPANY> (fan-out)
```

| Bukti | Perintah | Hasil |
|---|---|---|
| Framing 0x80 (start/length/proto/cmdLen/flag/content/serial/CRC/stop) | `go test ./controllers/ -run TestBuildGT06OnlineCommandFraming` | PASS (19 byte; CRC-ITU atas `length‖proto‖content`) |
| Whitelist `DYD#`/`HFYD#`/`TIMER,<s>#`/`RESET#`/`DWXX#` + tolak di luar itu | `-run 'TestGT06CommandContentWhitelist\|TestDeviceCommandValidate'` | PASS |
| Frame benar-benar ditulis ke socket perangkat | `-run TestDispatchWritesFrameToRegisteredDevice` | PASS (`net.Pipe`: byte `0x80 … "DYD#"`) |
| Device offline → `offline`; protokol tanpa encoder → `failed: unsupported` | `-run TestDispatchOfflineAndUnsupportedProtocol` | PASS |
| ACK perangkat tercatat (`DYD=Success!`→`acked`, `DYD=Unvalued Fix`→`failed`) | `-run TestAckCapturesDeviceReply` | PASS (log: `status=acked detail="DYD=Success!"`) |
| Tanpa ACK → `timeout` | `-run TestSweepExpiredMarksPendingAsTimeout` | PASS |
| Registry anti-race reconnect (socket lama tidak menghapus socket baru) | `-run TestConnRegistryReconnectSemantics` | PASS |
| Endpoint REST + baris audit + publish | `services/api-vehicle: go test ./... -run Command` | PASS (201 + status `pending` + request_id sama) |
| Tabel + constraint | §5 (SQL check) | PASS (command/status invalid ditolak CHECK) |
| Konsumsi durable (command tidak hilang saat service restart) | `scripts/e2e-commands.sh` (§7.1) + boot log `delivery=jetstream-durable` | PASS (`request_id` yang dipublikasikan saat ingestion-tcp mati tetap tercatat di `td_device_commands` setelah restart) |
| Satu perintah in-flight per device (balasan device tidak ambigu) | `-run TestDispatchRefusesSecondCommandWhileDeviceIsBusy` | PASS (perintah kedua `failed` dengan request_id yang menunggu; socket tidak disentuh; setelah ACK perintah berikutnya terkirim) |
| Batas umur perintah (`COMMAND_MAX_AGE_SECONDS`) terhadap replay JetStream | `-run TestHandleRequestDropsStaleCommand` | PASS (`failed` + alasan; 11 replay backlog nyata ditolak, §5) |
| Encoder TK103 (`AV010`/`AV011`/`AT00`/`AP00`/`AP07`/`AR00<4 hex>`/`AR0000000000`) | `-run TestTK103CommandEncoding` | PASS |
| Timestamp `sent_at`/`acked_at` per status (perbaikan `42P08`) | `-run TestCommandTransitionTimes` | PASS |
| Dua command baru masuk whitelist + CHECK DB (migrasi 024) | SQL check §5 | PASS (`device_version`/`position_stop` diterima, `open_trunk` ditolak CHECK) |

Catatan jujur: `GT06_COMMAND_LENGTH_INCLUDES_CRC=false` (default) mengikuti framing
client→server yang sudah terverifikasi; contoh vendor §8.1 menghitung 2 byte CRC di
`Length`. Balasan perangkat (`td_device_commands.detail`) yang memutuskan — toggle
sudah disediakan agar tidak perlu ubah kode saat device nyata diuji.

### 1.2 Driver behaviour — PRD §21.2 row 4

- **Harsh acceleration/braking/cornering** hanya ditulis bila frame membawa flag
  device (GT06 alarm `0x29`/`0x30` → `harsh_accel`/`harsh_braking`; Teltonika IO
  253/254/240); **tidak pernah** diinferensi dari selisih kecepatan.
- **Speeding** = episode terukur: dibuka saat `speed > limit` (tanpa grace), ditutup
  saat kembali ≤ limit; `duration_seconds` dari waktu perangkat; ambang
  `DRIVER_SPEEDING_MIN_SECONDS=10`.
- **Skor harian** `th_driver_scores` **dinormalisasi per jarak** sejak migrasi 025:
  bobot event (10/10/5/5) dibagi jarak tempuh hari itu (`th_vehicle_trips`,
  `deleted_at IS NULL`) → `events_per_100km`; bucket ≤5 → 100 (A), ≤15 → 85 (B),
  ≤25 → 75 (C), ≤40 → 65 (D), di atasnya turun linear sampai 0 (E). Bila jarak hari
  itu < `DRIVER_SCORE_MIN_DISTANCE_KM` (default 5) rumus berbasis jumlah event
  dipakai dan tetap disimpan di kolom `score_by_counts` untuk audit.
  Alert `driver_event` dengan dedup `driver:<company>:<vehicle>:<type>`.


| Bukti | Perintah | Hasil |
|---|---|---|
| Formula skor + grade + floor 0 | `go test ./controllers/ -run TestScoreFromCounts` | PASS |
| Normalisasi per jarak (bucket + fallback + floor) | `-run TestScoreFromDistance` | PASS (400 km → 100/A, 200 km → 85/B, 40 km → 60/D, ekstrem → 0/E, tanpa jarak → rumus jumlah) |
| Worker memakai jarak trip hari itu + simpan `score_by_counts` | `-run TestRefreshDriverScoreUsesDistance` | PASS |
| Pulse device → event + skor harian | `-run TestDetectDriverRecordsDevicePulses` | PASS (raw_code `0x30`, timestamp device, skor 90/A) |
| Episode speeding 45 s ditulis sekali | `-run TestSpeedingEpisodeIsMeasuredAndClosedOnce` | PASS (45 s, limit 60 tanpa grace) |
| Blip 3 s diabaikan | `-run TestSpeedingEpisodeShorterThanThresholdIsDropped` | PASS |
| **E2E device nyata** alarm `0x26/0x30` + trip 200 km | §2.6 | PASS (`distance_km=200`, `events_per_100km=5`, `score=100/A`, `score_by_counts=90`) |

### 1.3 Maintenance scheduling — PRD §21.2 row 5

`tm_maintenance_schedules` + `td_maintenance_logs` (migrasi `023`); `worker-alert`
mengevaluasi tiga dimensi (km, engine hours, kalender) dengan margin pengingat tiap
`MAINTENANCE_SWEEP_SECONDS`, lalu alert `maintenance_due` + `last_reminder_at`
(cooldown `MAINTENANCE_REMINDER_COOLDOWN_HOURS`).

| Bukti | Perintah | Hasil |
|---|---|---|
| Tiga ambang + margin | `go test ./controllers/ -run TestMaintenanceDueThresholds` | PASS |
| Reminder terangkat + cooldown | `-run TestSweepMaintenanceRaisesReminderAndStampsCooldown` | PASS |
| Tabel + FK/CHECK | §5 SQL check | PASS |

> CRUD/UI maintenance tetap milik B12 (PRD §5.10 1.4: "maintenance menyambung B8");
> B8 memiliki *reminder engine*-nya.

## 2. B9 — Protocol Expansion

### 2.1 Arsitektur decoder pluggable

`services/ingestion-tcp/controllers/decoder.go` — interface `Decoder`
(`Protocol()`, `Port(cfg)`, `Serve`) + registry `RegisterDecoder`. `main.go` tidak
lagi punya daftar protokol tetap: listener dibangun dari `RegisteredDecoders()`,
jadi menambah keluarga device = menambah satu file + satu `RegisterDecoder` (dan env
port-nya). Protokol dipilih oleh **listener/port**, bukan sniffing byte pertama.

11 listener aktif (dev): `9003 gt06 · 9011 teltonika · 9013 tk103 · 9002 meiligao ·
9004 xexun · 9017 suntech · 9010 h02 · 9005 totem · 9006 gt02 · 9012 navigil ·
9019 castel` (semua tercatat di boot `logs/ingestion-tcp.log`).

### 2.2 Status per keluarga

| Protokol | Port dev | Login/identitas | Position | Catatan |
|---|---|---|---|---|
| TK103 | 9013 | `##,imei:<15>,A` → `LOAD` | `imei:<15>,tracker,…` | subset posisi; matriks perintah TK103 (alarm/RFID/BMS/OBD) belum |
| Meiligao | 9002 | BCD 7 byte (14 digit → dinormalkan 15) | kalimat ASCII NMEA + alarm | sesuai decoder Traccar (`decodeRegular`) |
| Xexun | 9004 | `imei:<15>` | GPRMC/GNRMC basic+full; knots→km/h; status ACC/SOS | **end-to-end terbukti** (§2.3) |
| H02 | 9010 | `<IMEI>` per frame | teks `V3`/`VP1`, bit10 = ACC | mode biner (`$`) belum |
| Totem | 9005 | pipe-delimited | `$$…$GPRMC…` (PATTERN_1) | PATTERN_2 (tanpa GPRMC) belum |
| GT02 | 9006 | 8 byte hex (nibble marker dibuang) | `0x10` data + `0x1A` heartbeat | checksum XOR menerima dua rentang (ambiguitas dokumen) |
| Suntech | 9017 | teks; ID = IMEI 15 digit | **teks universal klasik** (ST215/ST300STT) | kalimat: `header;id;versi;YYYYMMDD;HH:MM:SS;[cell;]lat;lon;speed;course` — lat/lon bertanda, speed km/h; varian biner/per-model + CRR/HTE tetap dihitung |
| Navigil | 9012 | header 20 B LE, device id 4 B + ACK 24 B | MSG 8 (unit report) + MSG 18 (tracking) | peta `NAVIGIL_DEVICE_MAP` (id→IMEI) karena allowlist berbasis IMEI; MSG 13/15 tetap dihitung |
| Castel | 9019 | header `0x40 0x40` LE (Length = **seluruh frame**) + ID 20 ASCII | — | IMEI diekstrak dari ID; balasan login/heartbeat 0x9001/0x9003 dikirim; skala lat/lon GPS tetap tidak ditebak |

Test vector: `services/ingestion-tcp/controllers/protocols_test.go` (helper NMEA,
IMEI hex/BCD, framing Meiligao + checksum rusak, Xexun, H02 V3, TK103, GT02 posisi +
hemisphere, Totem, Navigil header+ACK+payload+peta device id, Castel framing
dua-frame berurutan + respons login/heartbeat, TK103 encode perintah GT06/TK103).

```
cd services/ingestion-tcp && go test ./controllers/ -run 'TestNMEA|TestHexIMEI|TestParseXexun|TestApplyXexun|TestMeiligao|TestParseH02|TestParseTK103|TestGT02|TestParseTotem|TestNavigil|TestCastel|TestTK103Command'
ok  adatrack_gps/ingestion-tcp/controllers
```

Perbaikan hasil audit (rincian di §6):
`NavigilProtocolDecoder` upstream memakai ACK 24 byte (header 20 + data 4 byte
`seq`/`status`, CRC hanya atas data) — implementasi awal mengirim 20 byte tanpa data
sehingga device akan menolaknya; sudah diperbaiki + diuji. Castel memakai `Length`
= ukuran frame total (dibuktikan dari pembentukan respons upstream: 31 byte
heartbeat, 41 byte login) — pembaca awal menghitungnya sebagai "byte setelah header"
sehingga sesi multi-frame desync 4 byte tiap frame; sudah diperbaiki + diuji.

### 2.3 Bukti end-to-end device non-GT06 (acceptance B9)

```
kirim ke 127.0.0.1:9004 (Xexun):
  GPRMC,063519.000,A,0612.0000,S,10650.0000,E,022.4,084.4,240926,,,A*00,imei:864201040512345;

logs/ingestion-tcp.log:
{"msg":"ingestion authenticated","protocol":"xexun","imei":"864201040512345",
 "company":"DEV001","vehicle_id":1,"remote":"127.0.0.1:43508"}

adatrack_gps_dev001.th_telemetry_logs:
imei=864201040512345 lat=-6.20000000 lon=106.83333333 speed=41.4848
acc_status=NULL timestamp=2026-09-24 06:35:19

Redis adatrack_gps:dev001:vehicle:state:864201040512345:
{"lat":-6.2,"lon":106.83333333333333,"speed":41.4848,"heading":84,"fix":true,
 "status":"ONLINE","imei":"864201040512345","vehicle_id":1}
```

Angka cocok rumus: `0612.0000 S → -(6 + 12/60) = -6.2`; `10650.0000 E → 106 + 50/60
= 106.833333`; `022.4 kn × 1.852 = 41.4848 km/h`. `acc_status` tetap **NULL** (frame
tanpa ACC) → kontrak tri-state B6 ikut terjaga untuk protokol baru. worker-live /
worker-persistence / worker-alert **tidak diubah sama sekali**.

### 2.4 Bukti end-to-end Navigil (gap yang ditutup audit lanjutan)

Frame `MSG_UNIT_REPORT` (8) dikirim ke listener Navigil `:9012` dengan
`NAVIGIL_DEVICE_MAP=1234567=864201040512345`:

```
ack_len=24 ack=01000100ff0018000000edd500000000d592b56a07000000
ack_seq=7 ack_status=0 msg_id=255 crc_ok=True          ← ACK 24 byte, CRC atas 4 byte data
sent_msgs=1 lat=-6.2000000 lon=106.8000000             ← int32/1e7 dari payload

logs/ingestion-tcp.log:
{"msg":"ingestion authenticated","protocol":"navigil","imei":"864201040512345",
 "company":"DEV001","vehicle_id":1}

adatrack_gps_dev001.th_telemetry_logs:
imei=864201040512345 latitude=-6.20000000 longitude=106.80000000 speed=0
altitude=45 acc_status=NULL timestamp=2026-09-24 21:14:36+00   ← waktu device

Redis adatrack_gps:dev001:vehicle:state:864201040512345:
{"lat":-6.2,"lon":106.8,"speed":0,"satellites":9,"altitude":45,"fix":true,"status":"ONLINE"}
```

Artinya keluarga Navigil kini memenuhi acceptance B9 (login→telemetry→persist→live
state) tanpa menyentuh worker-live / worker-persistence / worker-alert, dan identitas
device id 4 byte terselesaikan lewat peta konfigurasi (bukan tebakan).
Mapping dikembalikan ke kosong setelah pengujian (`NAVIGIL_DEVICE_MAP=`).

### 2.5 Bukti end-to-end Suntech teks (gap payload yang ditutup)

Kalimat universal klasik dikirim ke listener Suntech `:9017`:

```
ST300STT;864201040512345;1;20260924;06:35:19;ABCD;-06.20;+106.80;041.000;084.00;0#AB12

logs/ingestion-tcp.log:
{"msg":"ingestion authenticated","protocol":"suntech","imei":"864201040512345",
 "company":"DEV001","vehicle_id":1}

adatrack_gps_dev001.th_telemetry_logs:
imei=864201040512345 latitude=-6.20000000 longitude=106.80000000
speed=41 heading=84 acc_status=NULL timestamp=2026-09-24 06:35:19+00

Redis adatrack_gps:dev001:vehicle:state:864201040512345:
{"lat":-6.2,"lon":106.8,"speed":41,"heading":84,"fix":true,"status":"ONLINE"}
```

`speed` masuk sebagai km/h (satuannya memang km/h di wire, upstream mengonversi
km/h → knots — bukan sebaliknya) dan `acc_status` tetap NULL karena kalimat teks
Suntech tidak membawa status ACC (kontrak tri-state B6 terjaga).

### 2.6 Bukti end-to-end skor mengemudi per jarak (gap B8)

Perintah device asli (login GT06 → paket alarm `0x26` alasan `0x30` = harsh braking)
dikirim ke `:9003`, setelah satu trip B7.2 (200 km) dicatat untuk hari itu:

```
login_ack=78780401000000fedc0d0a
alarm_reply=787803263000ba530d0a

adatrack_gps_dev001.td_driver_events:
harsh_braking | high | device_alarm | raw_code=48 | speed=40.00 km/h

adatrack_gps_dev001.th_driver_scores:
period_start=2026-09-25 harsh_braking_count=1 distance_km=200.000
events_per_100km=5.000 score_by_counts=90.00 score=100.00 grade=A
```

10 poin (harsh braking) / 200 km × 100 = 5 poin per 100 km → bucket A. Kolom
`score_by_counts` menyimpan versi lama sehingga operator bisa melihat kedua angka;
baris uji dihapus kembali setelah verifikasi.


## 3. B10 — Normalisasi & Konfigurasi

### 3.1 Prefix tabel `tm_`/`th_`/`td_` (guard otomatis)

`internal/normalization_test.go` memindai seluruh `database/migrations/{master_pg,company_pg}/*.sql`
dan gagal bila ada `CREATE TABLE` yang bukan `tm_/th_/td_` atau berschema-qualified.

```
$ cd internal && go test ./ -run 'TestMigration|TestBusiness'
ok  adatrack_gps/internal
```

Tabel baru fase ini: `td_device_commands` (021), `td_driver_events` + `th_driver_scores`
(022), `tm_maintenance_schedules` + `td_maintenance_logs` (023).

### 3.2 Split user master & `business_type`

Diverifikasi test yang sama: `tm_users` (B2B, master `008`), `tm_users_b2c` (B2C,
`009`), `business_type` di `tm_companies` (`003`).

### 3.3 Config ganda LOCAL + COOLIFY

`docker-compose.{local,coolify}.yml` + `.env.{local,coolify,example}` mendapat blok
baru (`*_TCP_PORT` B9, `TELEMETRY_INTERVAL_SECONDS`, `COMMAND_*`, `DRIVER_*`,
`MAINTENANCE_*`, `INGESTION_CHECKSUM_STRICT`, `GT06_COMMAND_LENGTH_INCLUDES_CRC`).
LOCAL memakai port dev 9xxx, COOLIFY memakai konvensi Traccar 5xxx — tanpa edit manual.

### 3.4 Telemetry interval 20 s

`TELEMETRY_INTERVAL_SECONDS` (default 20) → `internal.Config.Telemetry`, divalidasi
`> 0` saat boot, diekspor sebagai metrik **`telemetry_interval_seconds`** di semua
service, dicatat di boot log ingestion, dan dipakai untuk command `TIMER,<detik>#`.

Bukti: `{"msg":"telemetry cadence configured","interval_seconds":20,
"env":"TELEMETRY_INTERVAL_SECONDS","metric":"telemetry_interval_seconds"}`
(`logs/ingestion-tcp.log`).

**Perbaikan audit lanjutan:** gauge-nya sempat bernilai **0** di `/metrics` karena
config dimuat sebelum service meregistrasi collector (`ObserveTelemetryInterval`
dipanggil saat gauge masih `nil`). Sekarang nilai terakhir diingat dan diterapkan
saat registrasi, dengan test `TestObserveTelemetryIntervalSurvivesLateRegistration`.
Bukti live sesudah perbaikan: port `8090/8091/8092/8094/8095` semuanya melaporkan
`telemetry_interval_seconds 20`.

### 3.5 Input validation + anti-attack hardening (§8.5/§9.6)

- Paket baru `internal/validate` (whitelist tertutup, batas panjang, kontrol
  karakter, rentang angka, pagination, token subjek NATS) + test.
- Diterapkan: IMEI wajib 15 digit saat membuat kendaraan (identitas anti-spoofing
  FR-1.4), `search` list kendaraan (≤100 char, tanpa kontrol), whitelist + rentang
  `interval_seconds` pada endpoint command (memakai paket yang sama).
- Hardening yang sudah ada dan tetap berlaku: request-id/recovery/security-headers/
  body-limit/CORS/rate-limit (api-vehicle); max-connections, idle timeout, batas
  panjang frame, validasi checksum (ingestion); anti-spoofing IMEI (semua decoder).

## 4. Gap jujur (belum selesai)

1. **B9 payload Castel GPS**: skala lat/lon `MSG_SC_GPS` (0x4001) tidak ada di rujukan
   yang bisa diambil utuh, jadi frame posisinya tetap dihitung
   `ingestion_unsupported_frames_total{protocol="castel"}` (bukan ditebak: skala yang
   salah menghasilkan posisi yang salah tanpa error). Framing, identitas dan balasan
   login/heartbeat sudah benar. Suntech **teks klasik sudah ditutup** (§2.5);
   varian biner/per-model (ST2xx/ST4xx/ST9xx, CRR, HTE) masih dihitung. Navigil
   ditutup untuk MSG 8/18; MSG 13/15 tetap terbuka.
2. **B9 cakupan**: Meiligao OBD/RFID, H02 biner, Totem PATTERN_2, dan sisa matriks
   perintah TK103 (85+ tipe upstream: handshake `BP00/BS50`, alarm/RFID/BMS/OBD,
   suhu) belum diimplementasikan. Yang sudah ada kini **7 perintah** TK103
   (`AV010`/`AV011`/`AT00`/`AP00`/`AP07`/`AR00<hex>`/`AR0000000000`); command
   destruktif `AX01` (reset odometer) sengaja **tidak** diekspos.
3. **B8 downlink**: encoder tersedia untuk GT06/Concox **dan TK103**; keluarga lain
   melaporkan `failed: unsupported` (eksplisit, tercatat). Pengiriman command memakai
   **JetStream durable consumer** (`ingestion-command-dispatch`, manual ack,
   `MaxDeliver=5`) dengan dua guard (satu in-flight per device, batas umur 300 s).
   **api-vehicle tetap butuh NATS saat boot** (fail-fast) karena endpoint command
   tidak bisa bekerja tanpanya.
4. **B8 skor mengemudi** sudah dinormalisasi per jarak (§1.2, migrasi 025). Sisa:
   atribusi skor ke **driver** (`driver_id`) masih NULL karena penugasan driver↔trip
   adalah modul B12; skor saat ini per kendaraan/hari.
5. **B10 validasi IMEI** hanya di create kendaraan (IMEI tidak bisa diubah via PATCH),
   sehingga binding lama (`min=5,max=30`) tetap ada demi kompatibilitas data lama
   sementara jalur create sudah ketat 15 digit.
6. **B9 identitas non-IMEI**: hanya Navigil yang punya jalur peta id→IMEI
   (`NAVIGIL_DEVICE_MAP`, salah ketik ditolak + dihitung
   `ingestion_unmapped_devices_total`). Suntech dengan id 6 digit dan Castel tanpa
   IMEI di dalam ID belum punya jalur onboarding; device seperti itu tidak akan
   pernah dianggap terautentikasi (dan kini tercatat, bukan gagal senyap).

## 5. Perbaikan hasil audit (2026-09-24, lanjutan)

| # | Temuan audit | Perbaikan | Bukti |
|---|---|---|---|
| 1 | ACK Navigil 20 byte tanpa data → device menolak/mengulang | ACK 24 byte (`seq`+`status`, CRC atas 4 byte data), timestamp +25 leap second, gerbang flag no-ack, counter kirim | `TestNavigilHeaderAndAck` |
| 2 | Payload Navigil tidak didekode | MSG 8 (unit report) + MSG 18 (tracking): lat/lon `int32/1e7`, speed km/h, course ×2, fix bit0; payload pendek ditolak | `TestParseNavigilPayload` |
| 3 | Identitas Navigil (device id) tak bisa dipetakan | `NAVIGIL_DEVICE_MAP` + validasi 15 digit + metrik `ingestion_unmapped_devices_total{protocol}` + boot log | `TestNavigilDeviceMap`, boot log `protocol identity mappings` |
| 4 | Castel: `Length` salah tafsir → desync 4 byte/frame, CRC+footer ikut jadi payload | Konvensi `Length` = seluruh frame (dibuktikan dari pembentukan respons upstream), tail dipisah, CRC diperiksa + `ingestion_castel_crc_errors_total` | `TestCastelFrameAndIdentity` (dua frame berurutan) |
| 5 | Castel tidak membalas login/heartbeat → device tidak pernah streaming | Balasan 0x9001 (dengan waktu server) & 0x9003; urutan byte type dapat diatur `CASTEL_RESPONSE_TYPE_BE` | `TestCastelResponseFrames` |
| 6 | Downlink memakai core NATS → command hilang saat service restart | Consume lewat JetStream durable + manual ack + `MaxDeliver=5` (`internal.NATSClient.QueueSubscribeDurable`), fallback tercatat | E2E §5.2 |
| 7 | (ditemukan saat audit lanjutan) INSERT audit command gagal `42P08` | Timestamp `sent_at`/`acked_at` dihitung di Go (`commandTransitionTimes`) alih-alih `CASE` atas parameter yang sama | `TestCommandTransitionTimes` + baris nyata di `td_device_commands` |
| 8 | Encoder downlink hanya GT06, keluarga TK103 dilaporkan unsupported | Encoder TK103 `AV010`/`AV011`/`AT00`/`AP00`/`AR00<4 hex>` + validasi rentang interval | `TestTK103CommandEncoding` |
| 9 | Downlink hanya bisa dipicu dari REST (sulit diuji operator) | `tools/jsadmin --publish-command …` + `make js-publish-cmd`, dan `scripts/e2e-commands.sh` (`make e2e-commands`) dengan simulator device GT06 | 5/5 PASS |
| 10 | (ditemukan E2E live) `Ack` memilih pending **tertua** untuk satu IMEI → balasan device meng-ACK perintah lama, perintah baru tersangkut `sent` | **Satu perintah in-flight per device**: perintah kedua ditolak eksplisit (`failed`, detail memuat request_id yang sedang menunggu) dan socket tidak disentuh — balasan device jadi tidak ambigu | `TestDispatchRefusesSecondCommandWhileDeviceIsBusy` |
| 11 | (ditemukan E2E live) JetStream dapat membuat ulang consumer → `DeliverAll` memutar ulang seluruh stream → perintah `engine_cut` berumur jam bisa terkirim ke kendaraan yang bergerak | Batas umur `COMMAND_MAX_AGE_SECONDS` (default 300): permintaan lebih tua dicatat `failed` + alasan, tidak pernah dikirim; boot log memuat `max_age_s` | `TestHandleRequestDropsStaleCommand` + bukti runtime di bawah |
| 12 | (ditemukan audit B10 live) metrik `telemetry_interval_seconds` selalu `0` di `/metrics` | Nilai yang diobservasi diingat lalu diterapkan saat collector diregistrasi (urutan config → registrasi tidak lagi penting) | `TestObserveTelemetryIntervalSurvivesLateRegistration` + `curl` 5 port = 20 |
| 13 | (audit GAP) Payload posisi Suntech hanya framing/identitas | Teks universal klasik didekode: header;id;versi;YYYYMMDD;HH:MM:SS;[cell;]lat;lon;speed;course (sign `[-+]` wajib, speed km/h, rentang koordinat divalidasi, id 6 digit ditolak) | `TestParseSuntechTextLine` + E2E §2.5 |
| 14 | (audit GAP) Matriks perintah TK103 kurang 2 perintah aman | `device_version` (`AP07`) + `position_stop` (`AR0000000000`) ditambahkan end-to-end (model, whitelist API, encoder, migrasi 024 + CHECK DB, `jsadmin`); `AX01` reset odometer sengaja tidak diekspos | `TestTK103CommandEncoding` + SQL check §5 |
| 15 | (audit GAP) Skor mengemudi tidak dinormalisasi per jarak | Skor harian = poin event per 100 km dari `th_vehicle_trips` (migrasi 025: `distance_km`/`events_per_100km`/`score_by_counts`), bucket A..E, fallback rumus jumlah di bawah `DRIVER_SCORE_MIN_DISTANCE_KM` | `TestScoreFromDistance`, `TestRefreshDriverScoreUsesDistance` + E2E §2.6 |

Bukti runtime guard #11 (replay backlog nyata, `logs/ingestion-tcp.log`):

```
{"msg":"downlink command dispatcher started",...,"max_age_s":300,"delivery":"jetstream-durable"}
{"msg":"downlink: stale request dropped",...,"command":"engine_cut",
 "request_id":"4f6df95eb1555349f9b76ef97f75f426","age_s":896.5,"max_age_s":300}
{"msg":"downlink: stale request dropped",...,"command":"reboot",
 "request_id":"be95852895b8bf44335d294f8cb307db","age_s":863.5,"max_age_s":300}
```

11 entri backlog (dari sesi uji sebelumnya) ditolak dengan alasan, dan E2E kembali
**5/5 PASS** sesudahnya.

## 6. Perintah verifikasi lengkap

```bash
# 1. Seluruh modul (vet + unit test)
scripts/test.sh                      # exit=0 (9 modul)

# 2. Migrasi + SQL pada PostgreSQL nyata (skema scratch, lalu dibuang):
#    021/022/023 idempoten; CHECK menolak command/status/event/score invalid;
#    td_driver_events."timestamp" NOT NULL terjaga

# 3. Migrasi tenant nyata + regresi pipeline penuh
scripts/e2e-pipeline.sh              # 5/5 PASS (021-023 diterapkan ke DEV001)

# 4. Device non-GT06 end-to-end (B9): Xexun :9004 → th_telemetry_logs + Redis (§2.3)

# 5. Guard normalisasi + validasi (B10)
(cd internal && go test ./ ./validate/... -count=1)

# 6. Downlink end-to-end (B8): login → 0x80 DYD# → balasan 0x21 → td_device_commands
scripts/e2e-commands.sh              # 5/5 PASS (§5.2)
```

### 7.1 Bukti E2E downlink (`scripts/e2e-commands.sh` → 5/5 PASS)

```
[PASS] command delivered + device ACK recorded (request_id=f46d2e03… detail='DYD=Success!')
[PASS] device received the server command frame (78780c800800000000445944…)
[PASS] device replied with the 0x21 online-command reply
[PASS] offline device recorded as status=offline (request_id=2f198cb4…)
[PASS] command published while ingestion-tcp was DOWN was delivered on restart
       (durable consumer, request_id=488a1db8…)
```

Simulator device adalah GT06 nyata (login `0x01` + balasan `0x21` berisi
`DYD=Success!`), jadi jalur yang diuji = jalur produksi: REST/NATS → encoder
`0x80` → socket → balasan device → `td_device_commands(acked)`; baris DB-nya
diverifikasi langsung (`status`, `detail`, `sent_at`, `acked_at`).


