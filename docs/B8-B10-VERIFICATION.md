# B8 / B9 / B10 — Verifikasi & Audit (2026-09-24)

> Ringkasan bukti nyata untuk fase **B8 (Advanced Fleet Features)**, **B9 (Protocol
> Expansion)** dan **B10 (Normalisasi & Konfigurasi)**. Semua klaim di bawah punya
> perintah + hasil yang bisa diulang; item yang belum lengkap ditulis eksplisit di
> §4 (gap), mengikuti aturan `.agent/01-global-rules.md`.
>
> **Revisi 2 (audit lanjutan 2026-09-24):** sebelas temuan audit sudah diperbaiki —
> ACK Navigil, payload Navigil MSG 8/18, peta identitas Navigil, framing+respons
> Castel, downlink durable JetStream, bug SQL audit command, encoder TK103,
> E2E downlink baru (`scripts/e2e-commands.sh`), plus dua temuan yang hanya muncul
> saat E2E live: ambiguitas balasan device saat dua perintah in-flight dan replay
> backlog JetStream. Rincian: §5.

## 0. Ringkasan

| Fase | Status | Bukti utama |
|---|---|---|
| **B8** Downlink `DYD#` / driver behaviour / maintenance | ✅ selesai (gap tersisa 2: encoder non-GT06/TK103, skor belum per-jarak) | registry koneksi + dispatcher `command.request.>` **durable JetStream** + ACK `0x21` → `td_device_commands`; encoder GT06 + TK103; alert `driver_event`/`maintenance_due`; migrasi `021`–`023`; E2E `e2e-commands.sh` 5/5 |
| **B9** Protocol expansion (9 keluarga) | 🟡 sebagian (7 keluarga ingest penuh, 2 framing-only + respons) | registry decoder pluggable; 11 listener; **Xexun end-to-end** → `th_telemetry_logs` + Redis live state; **Navigil** payload MSG 8/18 + peta identitas; **Castel** framing benar + balasan login/heartbeat; test vector per protokol |
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
| Encoder TK103 (`AV010`/`AV011`/`AT00`/`AP00`/`AR00<4 hex>`) | `-run TestTK103CommandEncoding` | PASS |
| Timestamp `sent_at`/`acked_at` per status (perbaikan `42P08`) | `-run TestCommandTransitionTimes` | PASS |

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
- Skor harian `th_driver_scores` dari agregat `td_driver_events`
  (`scoreFromCounts`: 10/10/5/5 poin, grade A..E), alert `driver_event` dengan
  dedup `driver:<company>:<vehicle>:<type>`.

| Bukti | Perintah | Hasil |
|---|---|---|
| Formula skor + grade + floor 0 | `go test ./controllers/ -run TestScoreFromCounts` | PASS |
| Pulse device → event + skor harian | `-run TestDetectDriverRecordsDevicePulses` | PASS (raw_code `0x30`, timestamp device, skor 90/A) |
| Episode speeding 45 s ditulis sekali | `-run TestSpeedingEpisodeIsMeasuredAndClosedOnce` | PASS (45 s, limit 60 tanpa grace) |
| Blip 3 s diabaikan | `-run TestSpeedingEpisodeShorterThanThresholdIsDropped` | PASS |

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
| Suntech | 9017 | teks `<len>;<IMEI>;<cmd>;<data>#` | — | framing + identitas + anti-spoofing; payload posisi tak terdokumentasi → dihitung `ingestion_unsupported_frames_total` |
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

1. **B9 payload minima**: Suntech (text+binary) dan Castel (skala lat/lon GPS) hanya
   framing + identitas + respons; frame posisinya dihitung
   `ingestion_unsupported_frames_total{protocol=…}` dan di-log, bukan ditebak.
   Rujukan yang tersedia hanya memuat urutan field tanpa skala/offset, dan skala
   yang salah menghasilkan posisi yang salah tanpa error — jadi keluarga ini sengaja
   menunggu dokumen vendor sebelum diaktifkan. (Navigil sudah **ditutup** untuk MSG 8
   dan MSG 18 sejak audit 2026-09-24 lanjutan; MSG 13/15 tetap terbuka.)
2. **B9 cakupan**: TK103 (85+ tipe perintah), Meiligao OBD/RFID, H02 biner, Totem
   PATTERN_2 belum diimplementasikan.
3. **B8 downlink**: encoder tersedia untuk GT06/Concox **dan TK103** (`AV010`/`AV011`/
   `AT00`/`AP00`/`AR00<hex>`); keluarga lain melaporkan `failed: unsupported`
   (eksplisit, tercatat). Pengiriman command kini memakai **JetStream durable
   consumer** (`ingestion-command-dispatch`, manual ack, `MaxDeliver=5`) sehingga
   command yang dipublikasikan saat ingestion-tcp mati tetap terkirim setelah
   service hidup kembali; fallback ke core NATS hanya bila stream/consumer tidak
   tersedia (tercatat `delivery=jetstream-durable|core-nats` di boot log).
   **api-vehicle tetap butuh NATS saat boot** (fail-fast) karena endpoint command
   tidak bisa bekerja tanpanya.
4. **B8 skor mengemudi** belum dinormalisasi per jarak (butuh agregat trip B7.2 →
   modul Safety B12); formula saat ini berbasis jumlah event per hari.
5. **B10 validasi IMEI** hanya di create kendaraan (IMEI tidak bisa diubah via PATCH),
   sehingga binding lama (`min=5,max=30`) tetap ada demi kompatibilitas data lama
   sementara jalur create sudah ketat 15 digit.
6. **B9 identitas non-IMEI**: hanya Navigil yang punya jalur peta id→IMEI
   (`NAVIGIL_DEVICE_MAP`, salah ketik ditolak + dihitung
   `ingestion_unmapped_devices_total`). Suntech format lama (id 6 digit) dan Castel
   tanpa IMEI di dalam ID belum punya jalur onboarding; device seperti itu tidak
   akan pernah dianggap terautentikasi.

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


