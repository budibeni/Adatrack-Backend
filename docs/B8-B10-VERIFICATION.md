# B8 / B9 / B10 — Verifikasi & Audit (2026-09-24)

> Ringkasan bukti nyata untuk fase **B8 (Advanced Fleet Features)**, **B9 (Protocol
> Expansion)** dan **B10 (Normalisasi & Konfigurasi)**. Semua klaim di bawah punya
> perintah + hasil yang bisa diulang; item yang belum lengkap ditulis eksplisit di
> §4 (gap), mengikuti aturan `.agent/01-global-rules.md`.

## 0. Ringkasan

| Fase | Status | Bukti utama |
|---|---|---|
| **B8** Downlink `DYD#` / driver behaviour / maintenance | ✅ selesai (2 gap tercatat) | registry koneksi + dispatcher `command.request.>` + ACK `0x21` → `td_device_commands`; alert `driver_event`/`maintenance_due`; migrasi `021`–`023` |
| **B9** Protocol expansion (9 keluarga) | 🟡 sebagian (6 keluarga ingest penuh, 3 framing-only) | registry decoder pluggable; 11 listener; **Xexun end-to-end** → `th_telemetry_logs` + Redis live state; test vector per protokol |
| **B10** Normalisasi & konfigurasi | ✅ selesai | guard prefix `tm_/th_/td_` otomatis; `TELEMETRY_INTERVAL_SECONDS` + metrik gauge; config ganda LOCAL/COOLIFY; `internal/validate` (§8.5/§9.6) |

E2E regresi pipeline setelah refactor ingestion: **5/5 PASS**.

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
| Navigil | 9012 | header 20 B LE + ACK sekuens | — | payload posisi tak terdokumentasi; identitas device id 4 B (bukan IMEI) → butuh peta id→IMEI |
| Castel | 9019 | header `0x40 0x40` LE + ID 20 ASCII | — | IMEI diekstrak dari ID; skala lat/lon GPS tak terdokumentasi → tidak ditebak |

Test vector: `services/ingestion-tcp/controllers/protocols_test.go` (helper NMEA,
IMEI hex/BCD, framing Meiligao + checksum rusak, Xexun, H02 V3, TK103, GT02 posisi +
hemisphere, Totem, Navigil header+ACK, Castel framing).

```
cd services/ingestion-tcp && go test ./controllers/ -run 'TestNMEA|TestHexIMEI|TestParseXexun|TestApplyXexun|TestMeiligao|TestParseH02|TestParseTK103|TestGT02|TestParseTotem|TestNavigil|TestCastel'
ok  adatrack_gps/ingestion-tcp/controllers
```

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

1. **B9 payload minima**: Suntech (text+binary), Navigil (payload + identitas device
   id 4 B), Castel (skala lat/lon GPS) hanya framing + identitas; frame-nya dihitung
   `ingestion_unsupported_frames_total{protocol=…}` dan di-log, bukan ditebak.
2. **B9 cakupan**: TK103 (85+ tipe perintah), Meiligao OBD/RFID, H02 biner, Totem
   PATTERN_2 belum diimplementasikan.
3. **B8 downlink**: encoder baru GT06; keluarga lain melaporkan `failed: unsupported`
   (eksplisit, tercatat). Pengiriman command memakai core NATS (bukan JetStream
   durable) → command yang dipublikasikan saat ingestion-tcp mati tetap `pending`
   dan harus dikirim ulang operator. **api-vehicle kini butuh NATS saat boot**
   (fail-fast) karena endpoint command tidak bisa bekerja tanpanya.
4. **B8 skor mengemudi** belum dinormalisasi per jarak (butuh agregat trip B7.2 →
   modul Safety B12); formula saat ini berbasis jumlah event per hari.
5. **B10 validasi IMEI** hanya di create kendaraan (IMEI tidak bisa diubah via PATCH),
   sehingga binding lama (`min=5,max=30`) tetap ada demi kompatibilitas data lama
   sementara jalur create sudah ketat 15 digit.

## 5. Perintah verifikasi lengkap

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
```

