# B6 + B7 Verification — Real-Time Data Hardening & Fleet Management Core

> Bukti verifikasi nyata fase **B6** (audit fix data real-time) dan **B7**
> (fleet management core: odometer/engine-hours, trip/stop, reverse geocoding,
> point reduction). Semua angka berasal dari perintah yang dijalankan di mesin dev
> ini (infra compose nyata: PostgreSQL/Redis/NATS).
>
> Referensi: PRD FR-2.1 (live state), FR-2.5 (odometer/engine hours), FR-2.6
> (trip & stop), §5.9.3 (history playback), §6.2 (skema), §10.1 (metrik).
> Checklist: `.agent/03-backend-phases.md`; ringkasan fase: `.agent/02-roadmap-overview.md`.

## 0. Ringkasan

| Item | Status | Bukti |
|---|---|---|
| **B6** ACC tri-state (data device, bukan inferensi) | ✅ | §1 + unit test 5 modul |
| **B6** DTO `VEHICLE_UPDATE` lengkap (fuel/acc/sats/alt/gsm) | ✅ | §1.3 |
| **B7.1** Odometer & engine hours (worker-live → `tm_vehicles`) | ✅ | §2 + `make e2e-fleet` |
| **B7.2** Trip & stop detection (`th_vehicle_trips`/`td_vehicle_stops`) | ✅ | §2 + `make e2e-fleet` |
| **B7.3** Reverse geocoding offline (cache + fallback) | ✅ (gap seed, §3) | §3 + E2E |
| **B7.4** Point reduction RDP untuk playback | ✅ | §4 + E2E |
| Migrasi 018/019/020 terpasang di dua tenant | ✅ | §2.0 |
| E2E fase | ✅ | `make e2e-fleet` **10/10 PASS** (§5) |

---

## 1. B6 — Real-Time Data Hardening

### 1.1 Temuan audit & perbaikan

Checklist B6 meminta **ACC memakai data asli device, bukan inferensi `Speed > 0`**.
Pencarian kode menunjukkan inferensi itu sudah hilang (B2/B5a), **tetapi masih ada
inferensi lain yang sama damagingnya**: `acc` dipublikasikan sebagai `bool` biasa
(dengan `omitempty`), sehingga **frame yang tidak membawa ACC sama sekali** (kalimat
fuel-only `!AIOIL`, alarm LBS 0x19, atau device Teltonika tanpa IO ignition) tetap
sampai ke klien sebagai `acc:false` — nilai yang tidak pernah dilaporkan device.

Perbaikan (tri-state ACC end-to-end):

| Lapisan | Sebelum | Sesudah |
|---|---|---|
| ingestion-tcp `TelemetryMessage.ACC` | `bool` + `omitempty` | `*bool` (nil = device tidak melaporkan) |
| GT06 0x22 / 0x26 decoder | `t.ACC = data[26] == 1` | `models.BoolPtr(...)` (tetap byte device) |
| Teltonika IO 66/67/239/1 | `val == 1` (IO absen → false) | pointer hanya saat IO benar-benar ada |
| worker-live live state | `acc := t.ACC; ACC: &acc` | `ACC: t.ACC` (nil tetap nil, key hilang dari JSON) |
| worker-persistence `acc_status` | `NOT NULL DEFAULT 0` | nullable (migrasi `020`); tak dilaporkan → SQL **NULL** |
| worker-alert gate `FUEL_DROP_REQUIRE_ACC` | `!t.ACC` | `AccOn(t.ACC)` → fail-safe saat ACC tak dilaporkan |
| service-websocket DTO/playback | selalu `acc` | `acc` hanya bila nilainya nyata (tri-state juga di history/playback) |

### 1.2 Perintah & hasil (unit test)

```
$ cd services/ingestion-tcp && go test ./...        # PASS (termasuk TestACCIsTriState)
$ cd services/worker-live  && go test ./...         # PASS (TestBuildStateACCIsTriState)
$ cd services/worker-persistence && go test ./...   # PASS (models_test.go: NULL vs 0/1)
$ cd services/worker-alert && go test ./...         # PASS (gate menolak ACC tak dilaporkan)
$ cd services/service-websocket && go test ./...    # PASS (b6_realtime_test.go)
$ scripts/test.sh                                   # 12 modul `ok`, 0 FAIL
```

Test baru yang menahan regresi:

* `services/ingestion-tcp/controllers/acc_tristate_test.go` — ACC byte 0x01/0x00 →
  nilai literal; kalimat fuel & alarm LBS → `nil`; payload JSON tanpa key `acc`.
* `services/worker-persistence/models/models_test.go` — `acc_status` 1/0/NULL.
* `services/worker-live/controllers/worker_test.go` — live state: `acc=false` walau
  `speed>0`; status tetap ONLINE (ACC tidak diinferensi dari speed).
* `services/service-websocket/controllers/b6_realtime_test.go` — DTO FR-5.2 memuat
  `fuel_level`, `fuel_volume`, `fuel_temp_c`, `satellites`, `altitude`, `gsm_signal`;
  key `acc` **hilang** saat device tidak melaporkannya.

### 1.3 REST enrich live-state (sudah selesai di B5a)

`GET /api/v1/vehicles` + `/{id}` tetap meng-overlay `fuel_level`/`acc` dari Redis
dalam **satu batched MGET** (`live` block). B6 tidak mengubahnya dan tidak ada
regresi (suite `vehicles_rest_test.go` + api-vehicle `live_test.go` hijau).

### 1.4 Catatan jujur

Jalur "device tidak melaporkan ACC" diuji pada level unit (decoder → Redis → DTO).
E2E device GT06 selalu mengirim byte ACC, sehingga E2E tidak bisa menghasilkan
`acc_status = NULL` lewat jalur device nyata; verifikasi NULL ada di unit test
`worker-persistence/models` + skema migrasi `020`.

---

## 2. B7.1 & B7.2 — Fleet Management Core

### 2.0 Migrasi & skema

| File | Isi |
|---|---|
| `company_pg/018_create_odometer_engine_hours.sql` | `tm_vehicles.odometer_km NUMERIC(12,3) NOT NULL DEFAULT 0`, `engine_hours` (sama), `odometer_updated_at`, CHECK `>= 0` (anti-rollback), indeks `odometer_updated_at` |
| `company_pg/019_create_vehicle_trips.sql` | `th_vehicle_trips` (header: start/end time+posisi, distance/max/avg speed, stop_count, duration, soft delete §6.0.1) + `td_vehicle_stops` (FK → trip, `ON DELETE CASCADE`) + indeks |
| `company_pg/020_telemetry_acc_nullable.sql` | `th_telemetry_logs.acc_status` boleh NULL (B6) + COMMENT |

Nomor `016` sudah dipakai B5b dan `017` adalah indeks telemetri B4, sehingga rencana
`016`/`017` pada dokumen fase bergeser ke **018/019** (nomor saja; isi tetap sesuai
FR-2.5/FR-2.6). `database/init-pg/03_company_setup.sql` kini menerapkan `017`–`020`
juga — sebelumnya bootstrap fresh DB (docker entrypoint) **melewatkan 017** (indeks
`timestamp` FR-3.5); itu ikut diperbaiki.

```
$ make migrate | tail -6
db:   apply company:adatrack_gps_dev001/018_create_odometer_engine_hours.sql
db:   apply company:adatrack_gps_dev001/019_create_vehicle_trips.sql
db:   apply company:adatrack_gps_dev001/020_telemetry_acc_nullable.sql
db: company:adatrack_gps_dev001: 3 new migration(s) applied
master|20|0
company:adatrack_gps_dev001|21|0
migrate: done

$ psql -Atc "select table_schema, count(*) from information_schema.columns
             where column_name in ('odometer_km','engine_hours') group by 1"
adatrack_gps_default|2
adatrack_gps_dev001|2
$ psql -Atc "select table_schema, table_name from information_schema.tables
             where table_name in ('th_vehicle_trips','td_vehicle_stops')"
adatrack_gps_default|td_vehicle_stops
adatrack_gps_default|th_vehicle_trips
adatrack_gps_dev001|td_vehicle_stops
adatrack_gps_dev001|th_vehicle_trips
$ psql -Atc "select is_nullable from information_schema.columns
             where table_name='th_telemetry_logs' and column_name='acc_status'"
YES
```

### 2.1 FR-2.5 — Odometer & engine hours

Implementasi: `internal/geo` (Haversine radius 6371 km) +
`services/worker-live/controllers/{fleet.go,fleet_flush.go,fleet_store.go}`.

* Akumulasi **delta jarak** per pasangan fix berurutan; nilai ditulis sebagai
  `UPDATE tm_vehicles SET odometer_km = odometer_km + Δ` (monotonik; anti-rollback
  tiga lapis: hanya delta positif, `WHERE $2::float8 >= 0`, dan CHECK `>= 0`).
* Guard FR-2.5 lengkap: pesan tanpa posisi (fuel-only/heartbeat) diabaikan,
  `VehicleID = 0` diabaikan, delta > `ODOMETER_MAX_JUMP_KM` (5 km) dibuang sebagai
  GPS jump, interval device > `ODOMETER_MAX_GAP_SECONDS` (300 s) tidak dikreditkan,
  timestamp mundur/sama tidak menghasilkan delta.
* Engine hours hanya dikreditkan untuk interval saat fix **sebelumnya**
  mengonfirmasi ACC ON, dan **tidak pernah** bila ACC tak dilaporkan (B6).
* Flush `FLEET_FLUSH_SECONDS` (30 s) atau lebih awal saat `FLEET_FLUSH_BATCH` (100)
  vehicle pending; kegagalan flush di-**re-buffer** (tidak ada delta hilang) dan
  dihitung di `fleet_flush_errors_total{stage}`.
* Metrik: `odometer_updates_total`, `engine_hours_updates_total`,
  `trip_stop_flush_size`. Readiness worker-live kini mencakup PostgreSQL.

### 2.2 FR-2.6 — Trip & stop detection

State machine `MOVING ↔ STOPPED` per vehicle (`tripState`):

* grace `TRIP_STOP_GRACE_SECONDS` (30 s) → stop baru **dikonfirmasi** setelah itu;
* minimum `TRIP_MIN_STOP_SECONDS` (60 s) → stop lebih pendek = blip: tidak membuat
  baris stop dan **tidak memecah trip**;
* auto-close `TRIP_MAX_STOP_SECONDS` (3600 s) → trip ditutup pada flush berikutnya
  (berbasis waktu server, sehingga device yang berhenti mengirim pun ditutup);
* trip ditutup pada **awal stop** (end_time = start stop); trip baru dibuka saat
  kendaraan bergerak lagi. `stop_count`, `distance_km`, `max/avg speed`, `duration`
  dihitung dari data nyata.
* Idempotensi: bila INSERT stop gagal, trip **tidak** diulang — `pendingTrip.tripID`
  dibawa sehingga flush berikutnya hanya mengulang stop.
* Metrik: `trip_events_total{company_code,event_type}` (`trip_start`/`trip_end`),
  `stop_events_total{company_code}`.

### 2.3 Unit + integration test

```
$ cd services/worker-live && go test ./... -count=1
ok  adatrack_gps/worker-live/controllers

$ ADATRACK_IT=1 go test ./controllers -run TestIT -count=1 -v | grep -- '--- PASS'
--- PASS: TestITAddMeteringAccumulates      (counter nyata di tm_vehicles)
--- PASS: TestITInsertTripAndStops          (th_vehicle_trips + td_vehicle_stops)
... (12 test IT lain tetap hijau)
```

Unit test penahan perilaku: `fleet_test.go` (Haversine, guard jump/gap, ACC-gated
engine hours), `fleet_trip_test.go` (siklus trip→stop→trip, blip, auto-close,
requeue), `fleet_flush_test.go` (persist, retry, retry-stops-only, degradasi tanpa
store), `fleet_store_it_test.go` (PostgreSQL nyata).

### 2.4 Temuan penting saat verifikasi (bug yang ditemukan E2E)

`AddMetering` versi pertama lolos kompilasi tetapi **tidak menambah apa pun**: pada
`odometer_km + $2` PostgreSQL menyimpulkan parameter `$2` bertipe `numeric`, dan pada
stack pgx ini `float64` yang di-bind ke parameter `numeric` (hasil inferensi, tanpa
cast eksplisit) terkirim sebagai **0**. Diagnostik terhadap DB nyata:

```
(a) SET odometer_km = odometer_km + $2          -> odometer = 0
(b) SET odometer_km = odometer_km + $2::float8  -> odometer = 0.333
(d) SELECT ($1 + 0)::float8  (0.333)            -> 0      (inferensi numeric)
(e) SELECT $1::numeric::float8 (0.333)          -> 0.333  (cast eksplisit OK)
(f) INSERT ... VALUES ($4) ke kolom numeric     -> -6.303 (jalur VALUES OK)
```

Perbaikan permanen: **semua** parameter float pada `AddMetering`/`InsertTrip`/
`InsertStops` di-cast eksplisit `::float8`, dengan komentar yang menjelaskan alasan
(agar tidak ada yang "merapikan" cast itu dan mematikan fitur secara diam). Jalur
batch INSERT worker-persistence (posisi telemetri) terbukti tidak terpengaruh —
dibuktikan oleh (f) dan data `th_telemetry_logs` hasil E2E (lat/lon/speed benar).

---

## 3. B7.3 — Reverse Geocoding (offline)

`internal/geo` menyediakan indeks wilayah (centroid) + service-websocket:

* `PostgresStore.RegionCentroids` membaca `tm_subdistricts`/`tm_districts`/
  `tm_cities`/`tm_provinces` **hanya** untuk baris yang punya koordinat, dengan
  hierarki nama lengkap; level paling spesifik yang tersedia yang dipakai.
* `geocoder` (B7.3): cache berlapis **in-memory → Redis → indeks PostgreSQL**
  (refresh `GEOCODE_INDEX_REFRESH_SEC`, TTL `GEOCODE_CACHE_TTL_SEC`, kunci dibulatkan
  3 desimal ≈ 110 m). Kebijakan spesifisitas: level "spesifik" (kota/kecamatan/desa)
  dipakai bila centroid-nya dalam `GEOCODE_SPECIFIC_MAX_KM` (30 km), jika tidak jatuh
  ke provinsi dalam `GEOCODE_MAX_DISTANCE_KM` (75 km).
* **Fallback eksplisit**: koordinat yang tidak bisa diresolusi (mis. laut) tetap
  menjawab HTTP 200 dengan `resolved=false` dan `address=""` — bukan error, bukan
  alamat karangan. Indeks kosong / master mati juga hanya menghasilkan
  `resolved=false` + `geocode_index_load_errors_total`.
* Endpoint baru `GET /api/v1/geocode/reverse?lat&lon` (RBAC tenant) + integrasi di
  playback (`points[].address/city/province`).
* Metrik: `geocode_requests_total{result}`, `geocode_index_size`,
  `geocode_index_load_errors_total`; `/healthz` mengekspos `geocode_index_size`.

### Gap seed yang terdokumentasi (jujur)

Seed wilayah Indonesia yang ada **hanya** memuat centroid provinsi (35/38) dan
kota/kabupaten (180/514); `tm_districts` dan `tm_subdistricts` **0 baris
berkoordinat** (diukur langsung: `select count(*) ... where latitude is not null`
→ 0 dan 0). Karena itu resolusi offline saat ini berhenti di level **kota/kabupaten**
(mis. koordinat Jakarta → `"Kota Tangerang Selatan, Banten"`, level=city — centroid
kota terdekat yang tersedia). Query sudah menyertakan distrik/desa, sehingga begitu
seed diperkaya koordinat, presisi naik **tanpa perubahan kode**. Temuan §21.1 PRD #1
("Reverse Geocoding belum diintegrasikan") kini menjadi: integrasi selesai, presisi
menunggu data koordinat kabupaten/kecamatan/desa.

---

## 4. B7.4 — Point Reduction (Ramer–Douglas–Peucker)

* `internal/geo/reduce.go`: `ReduceIndices`/`Reduce` iteratif (tanpa rekursi), jarak
  tegak lurus dalam meter terhadap segmen (proyeksi planar lokal), selalu
  mempertahankan titik pertama & terakhir; `tolerance <= 0` = tanpa reduksi.
* Endpoint baru `GET /api/v1/vehicles/{id}/playback` (`from`/`to`/`tolerance_m`,
  RBAC row-level seperti endpoint history):
  * memuat posisi **naik** (ASC) dengan `LIMIT PLAYBACK_MAX_POINTS + 1` untuk
    mendeteksi truncation (`truncated=true`, total dipotong ke batas),
  * `distance_km` dihitung dari **urutan asli** (reduksi tidak mengubah jarak),
  * `total_points`, `returned_points`, `reduction_percent`, `tolerance_m`,
  * geocoding B7.3 pada titik yang tersisa (selalu untuk titik awal/akhir;
    `GEOCODE_MAX_POINTS` membatasi sisanya).
* Unit test: `internal/geo/reduce_test.go` (garis lurus → 2 titik, apex
  dipertahankan, batas degenerat, monotonisitas toleransi) dan
  `services/service-websocket/controllers/playback_test.go` (endpoint nyata: reduksi,
  endpoint preserved, truncation, validasi, RBAC, cache geocoder).

---

## 5. E2E fase B7 — `make e2e-fleet`

Harness baru `tools/e2e-fleet` + `scripts/e2e-fleet.sh` + target `make e2e-fleet`
menggerakkan device GT06 nyata (login → 7 frame posisi: 3 bergerak, fuel-only,
2 diam, resume, GPS jump), lalu memverifikasi DB + REST:

```
$ make e2e-fleet
e2e-fleet: ingestion-tcp ready (:8090/healthz)
e2e-fleet: worker-live ready (:8091/healthz)
e2e-fleet: worker-persistence ready (:8092/healthz)
e2e-fleet: service-websocket ready (:8082/healthz)
e2e-fleet: running harness
[PASS] auth.login                         tenant admin session established
[PASS] device.registered                  imei=864201040512345 vehicle=1 company=DEV001
[PASS] metering.baseline                  odometer=0.000 km engine_hours=0.000
[PASS] device.drive                       7 position frames + fuel + GPS jump (expected 0.334 km)
[PASS] odometer.accumulated               delta=0.334 km (want ~0.334) engine_hours=+0.0610 h
[PASS] trip.persisted                     id=5 distance=0.222 km duration=60 s max=44.5 km/h stops=1
[PASS] stop.persisted                     duration=130 s (want ~130) lat=-6.20200 lon=106.80000
[PASS] playback.reduced                   total=21 returned=2 (90.5% reduced) address="Kota Tangerang Selatan, Banten"
[PASS] geocode.reverse                    "Kota Tangerang Selatan, Banten" level=city
[PASS] cleanup                            removed 1 trip(s) and restored the counters

B7 fleet E2E summary: 10/10 checks passed
B7 fleet E2E PASSED
```

Arti tiap assertion:

* `odometer.accumulated` — delta odometer **sama dengan** panjang rute yang
  direncanakan (0.334 km), sehingga GPS jump ~11 km terbukti dibuang oleh FR-2.5;
  engine hours terkredit hanya karena ACC ON.
* `trip.persisted` / `stop.persisted` — state machine FR-2.6 menutup trip pada awal
  stop (60 s) dengan 1 stop berdurasi 130 s (grace 30 s + min stop 60 s).
* `playback.reduced` — endpoint playback mengembalikan **2 dari 21** titik (reduksi
  90.5%) dengan titik awal/akhir membawa alamat offline (B7.3+B7.4 dalam satu
  respons). `distance_km` mengukur seluruh baris `th_telemetry_logs` pada jendela
  waktu (fixture kendaraan dipakai bersama run lain), bukan hanya rute run ini —
  objek assertion adalah jumlah titik/alamat/reduksi.
* `cleanup` — harness memulihkan `odometer_km`/`engine_hours` dan menghapus trip yang
  dibuatnya, sehingga run berulang tetap bersih (E2E idempoten).

---

## 6. Batasan & follow-up yang jujur

1. **Presisi reverse geocoding** terbatas pada level kota/provinsi karena seed
   wilayah tidak memuat koordinat kecamatan/desa (§3). Perbaikan **data** (bukan
   kode) akan langsung menaikkan presisi.
2. **`acc_status = NULL` lewat jalur device** belum di-E2E-kan: device GT06 selalu
   mengirim byte ACC. Jalur NULL diuji pada unit test + skema migrasi `020`.
3. **Playback memakai jendela waktu**, sehingga `distance_km` pada B7 E2E mencakup
   baris telemetri run lain untuk kendaraan fixture yang sama.
4. **Metrik baru** (`odometer_updates_total`, `engine_hours_updates_total`,
   `trip_events_total`, `stop_events_total`, `trip_stop_flush_size`,
   `fleet_flush_errors_total`, `geocode_*`) belum masuk dashboard/rule alert B4
   (`monitoring/`) — follow-up operasional.
5. **Retensi `th_vehicle_trips`/`td_vehicle_stops`** belum masuk job retensi B4;
   volumenya jauh di bawah telemetri, tetapi tetap perlu kebijakan (menyambung B11).
6. **`trip_stop_flush_size`** adalah gauge batch terakhir (bukan histogram), sesuai
   penyebutan PRD §10.1; bila butuh distribusi, tambahkan histogram terpisah.
