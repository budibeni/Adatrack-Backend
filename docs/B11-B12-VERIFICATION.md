# B11 & B12 — Verification Evidence (2026-09-26)

> Bukti nyata untuk fase **B11 (Governance & Data Lifecycle)** dan **B12 (Enterprise &
> Industry Modules)**. Setiap klaim di bawah punya perintah + hasil; yang belum selesai
> dicatat jujur di §4 (bukan dihitung selesai).

## 1. B11 — Governance & Data Lifecycle

### 1.1 Audit trail wajib `tm_audit_logs` (§9.4)

| Item | Bukti |
|---|---|
| Middleware audit untuk **SEMUA** mutation | `services/api-vehicle/controllers/audit_mw.go` (`auditMutationMiddleware`, dipasang pada group tenant setelah auth+RBAC) |
| Mapping action/entity | `auditActionFor` (`ENTITY_CREATED/UPDATED/SOFT_DELETED/RESTORED`, `ALERT_ACKNOWLEDGED/RESOLVED`, `COMMAND_REQUESTED`, `MENU_ACCESS_UPDATED`, `MODULE_LICENSE_SET`, …) |
| Penolakan ikut teraudit | `denyRequest` → `auditDenial` (`ACCESS_DENIED`, outcome `denied`) |
| Redaksi data sensitif | `redactAuditState` (`password/token/secret/hmac/api_key/credential` → `[REDACTED]`) |
| Tidak silent drop | retry + backoff, metrik `audit_write_errors_total`, dead-letter `notify.deadletter` |
| Endpoint baca trail | `GET /api/v1/audit-logs` (Admin, tenant-scoped) + audit `AUDIT_LOGS_VIEWED` |

```
$ cd services/api-vehicle && go test ./... -count=1
ok  	adatrack_gps/api-vehicle/controllers
```

Test yang mengunci perilaku ini: `TestAuditActionMapping`,
`TestAuditMiddlewareWritesEveryMutation` (6 mutation → 6 baris, GET → 0 baris),
`TestAuditMiddlewareRedactsSensitivePayload`, `TestAuditDisabledWritesNothing`,
`TestDenialIsAudited`.

### 1.2 Soft delete global + endpoint restore (§6.0.1)

* Sudah ada sejak B3 untuk `vehicles`, `geofences`, `routes`, `speed-configs`, `fuel-configs`
  (`POST /{resource}/{id}/restore`, Admin-only) dan B5b untuk media.
* B12 memperluas pola yang sama ke seluruh resource baru (`drivers`, `groups`, `personnel`,
  `cards`, `assets`, `incidents`, `organizations`, `maintenance`) lewat satu implementasi
  tabel-driven; resource log (`access-logs`, `maintenance-logs`) sengaja **append-only**
  (tanpa route mutasi) — dikunci `TestEnterpriseNormalize` (flag `Immutable`).

### 1.3 Auto-create admin tenant `Admin@123` (FR-5.5)

Sudah dipenuhi sejak B2 dan diverifikasi di sesi itu (31/31 PASS provisioning E2E):
`services/service-websocket/controllers/handlers_companies.go` → `internal/tenant.ProvisionCompany`
+ admin `admin@{code}.local` (bcrypt cost 12, `must_change_password=true`, audit
`ADMIN_USER_AUTOCREATED`). B11 tidak mengubahnya; hanya memastikan parameter
`PASSWORD_DEFAULT_TENANT_ADMIN` tersedia di dua varian config.

### 1.4 Migrasi DB otomatis Coolify (§14.5)

* `deployments/docker-compose.coolify.yml` → `x-app-env: MIGRATE_ON_BOOT: ${MIGRATE_ON_BOOT:-true}`
  (setiap service aplikasi meng-apply migrasi master+company saat boot; advisory lock +
  ledger + checksum guard).
* `scripts/migrate.sh` tetap menjadi pre-deploy hook Coolify (7 langkah, fail-fast, ledger
  verification) — `docs/DEPLOY_COOLIFY.md` §3.
* Bukti boot nyata: seluruh verifikasi di bawah memakai jalur migrasi yang sama
  (`internal.ApplyMigrations`, ledger `master|N|0`).

### 1.5 Dukungan protokol universal (Module 1c) — registrasi device lintas brand

| Item | Bukti |
|---|---|
| Registry protokol (11 keluarga: port + env + status) | `internal/protocol/registry.go` |
| Test invarian registry (port/env/status, Teltonika satu-satunya `own`) | `internal/protocol/registry_test.go` — `go test ./internal/protocol/...` **ok** |
| Katalog tersimpan di master | `database/migrations/master_pg/021_create_protocols.sql` (+ seed idempoten) |
| Protokol ikut ke allowlist anti-spoofing | `master_pg/022_imei_map_protocol.sql` (`tm_vehicle_imei_map.protocol`) |
| Protokol di fleet master | `company_pg/026_device_protocol.sql` (`tm_vehicles.protocol/protocol_port/brand`) |
| Validasi registrasi (brand tak dikenal → 400; port diturunkan server) | `resolveProtocol` + `TestResolveProtocol`, `TestCreateVehicleStoresProtocol`, `TestCreateVehicleRejectsUnknownProtocol` |

## 2. B12 — Enterprise & Industry Modules

### 2.1 Registry modul/menu + akses menu per-tenant

| Endpoint | Keterangan |
|---|---|
| `GET /api/v1/access/menu` | navigasi milik **role pemanggil** (master `tm_menus` ∩ `tm_role_menu_access` ∩ lisensi modul) |
| `GET /api/v1/access/menu/role/{role}` | matriks lengkap role→menu (Admin) |
| `PUT /api/v1/access/menu/role/{role}` | ganti matriks atomik (Admin, teraudit `MENU_ACCESS_UPDATED`) |
| `GET /api/v1/modules` | lisensi modul tenant (core ON, industry opt-in) |
| `PUT /api/v1/modules/{code}` | set lisensi modul (Admin, teraudit `MODULE_LICENSE_SET`) |

Bukti: `TestAccessMenuUsesCallerRole`; data nyata `tm_role_menu_access` role **Admin = 86 baris** (§3).

### 2.2 CRUD enterprise tabel-driven

Resource: `drivers`, `groups`, `personnel`, `cards`, `access-logs`, `assets`, `incidents`,
`organizations`, `maintenance`, `maintenance-logs` (`enterprise_registry.go`).

Satu registri spesifikasi menghasilkan `GET / POST / GET{id} / PATCH / DELETE / RESTORE`
dengan RBAC (write = Admin/Manager, delete+restore = Admin), validasi per-field
(required/enum/panjang/tipe/waktu), soft delete, audit, dan `error_code` per-resource
(`DRIVER_NOT_FOUND`, …). Identifier SQL hanya berasal dari whitelist compile-time — tidak ada
nilai yang dikendalikan klien (§9.6); field tak dikenal ditolak (`TestComplianceGuardBlocksUnknownFields`).

```
$ go test ./controllers/ -run 'Enterprise' -count=1
ok  	adatrack_gps/api-vehicle/controllers
```

### 2.3 Mapping grup (vehicle/driver)

`GET|POST /api/v1/groups/{id}/members`, `DELETE /api/v1/groups/{id}/members/{memberId}`.
Upsert idempoten lewat partial unique index
(`ON CONFLICT (group_id, member_type, member_id) WHERE deleted_at IS NULL`) — diverifikasi SQL nyata (§3).

### 2.4 Analisis, keamanan, share, heatmap

| Endpoint | Sumber data |
|---|---|
| `GET /api/v1/reports/trips` | `th_vehicle_trips` |
| `GET /api/v1/reports/violations` | `td_driver_events` (B8) |
| `GET /api/v1/safety/scores` | `th_driver_scores` (B8) |
| `GET /api/v1/heatmap` | `tm_heatmap_cells` (cache) |
| `POST /api/v1/heatmap/rebuild` | agregasi `th_telemetry_logs` (Admin) |
| `GET|POST /api/v1/share-links`, `DELETE /api/v1/share-links/{id}` | master `tm_share_links` |
| `GET /api/v1/share/{token}` | **publik** (tanpa auth), TTL + revokasi, `view_count` |

Bukti: `TestPublicShareLifecycle` (201 → publik 200 tanpa identitas → token tak dikenal 404
`SHARE_LINK_NOT_FOUND`), plus agregasi SQL nyata (§3).
Integrations (§1.8): `GET/POST/PATCH/DELETE /api/v1/integrations` — rahasia hanya dikembalikan
sekali (disimpan sebagai SHA-256) dan dihapus = langsung `disabled`.

### 2.5 Lisensi industri (opt-in per tenant)

`tm_company_modules` (master `023`): modul core ON, modul §1.7 (rental/transport/logistics/sales/
field-service/patrol/project-site) OFF sampai diaktifkan; menu industri otomatis hilang dari
`GET /api/v1/access/menu` bila lisensi OFF (`moduleLicensed`).

## 3. Verifikasi database nyata (PostgreSQL dev :5533)

`psql -v ON_ERROR_STOP=1 -f /tmp/b12_verify.sql` — **transaksi di-ROLLBACK** supaya ledger migrasi
tetap konsisten. Enam file migrasi baru juga diuji apply+ROLLBACK per skema: **semua OK**.

```
--- apply master 023/024 ……………………………… OK (33 baris lisensi core + 21 industri)
--- apply company 026/027 …………………………… OK (11 tabel + 24 indeks)
--- module licence read ………………………………… 11 modul
--- module licence upsert (ON CONFLICT) ……… INSERT 0 1
--- group member upsert (partial unique) …… INSERT 0 1
--- share link create + resolve (TTL) ……… 1 baris, view_count = 1
--- heatmap rebuild ………………………………………… INSERT 0 12 → 12 sel (dari telemetri nyata)
--- trip report aggregate …………………………… 3 trip · 0.222 km · avg 4.44 · max 44.45 · 1 stop
--- role menu matrix read (role Admin) …… 86 baris
--- enterprise list projection (tm_drivers)  OK
ROLLBACK
B12 verification: OK (rolled back)
```

## 4. Yang BELUM selesai (dicatat jujur — tidak dihitung)

1. **B12 · halaman per-modul industri** (§1.7: rental/transport/logistics/sales/field-service/
   patrol/project-site, ±45 sub-halaman) — yang ada: registry + lisensi + gating menu.
   CRUD per sub-halaman (customers/orders/shipments/work-orders/checkpoints/…) belum dibuat;
   disarankan tabel bertipe per modul mengikuti pola `tm_`/`th_`/`td_` saat modul dijual.
2. **B12 · Personal/B2C (FR-9.2)** — `tm_users_b2c` ada (B10) tetapi auth B2C + endpoint
   Statistics/Settings belum dibuat.
3. **B12 · Reports lanjutan** — export (CSV/PDF) & laporan terjadwal belum ada; tersedia baru
   ringkasan trip/violation on-demand.
4. **B12 · Settings tenant** — endpoint preferensi per-tenant belum ditambah.
5. **B11 · audit asinkron** — api-vehicle menulis audit sinkron (retry + dead-letter, pola
   service-media) alih-alih buffer `AUDIT_BUFFER_SIZE`/`AUDIT_FLUSH_INTERVAL_MS` seperti
   service-websocket. Aman (tanpa drop), tetapi menambah sedikit latensi mutasi.
6. ~~**E2E harness**~~ — ✅ **SELESAI 2026-09-29**: `scripts/e2e-enterprise.sh` (`make e2e-enterprise`)
   menjalankan E2E HTTP penuh (login → CRUD → share publik → audit) → **31/31 PASS**, dan
   langsung menemukan + menutup 3 bug store Postgres (§4c).

## 4b. Verifikasi runtime & perbaikan jalur deploy (2026-09-29)

Audit runtime menemukan **dua defect jalur deploy** (bukan bug fitur): kode sudah sampai B12
sementara schema DB dev belum menerima migrasinya, dan skrip migrasi tidak menjangkau seluruh
tenant. Keduanya diperbaiki & diverifikasi live.

### 4b.1 Temuan: schema drift (kode ∥ DB)
- Sebelum: ledger dev **master 020 / company 025** → migrasi `021`–`024` & `026`–`027` belum
  di-apply. Dampak nyata: `GET /api/v1/audit-logs` & `GET /api/v1/access/menu` → **404**, dan
  `ADATRACK_IT=1 go test` api-vehicle **10/10 FAIL** (kolom `protocol` absen di `tm_vehicles`).
- Koreksi klaim lama: "`scripts/test.sh` (ADATRACK_IT=1) 0 FAIL" **tidak akurat** — `test.sh`
  tidak men-set `ADATRACK_IT=1`, jadi IT test selalu di-skip (mode hermetic).

### 4b.2 Fix #1 — `start-services.sh` mewajibkan migrasi sebelum boot
`make services-up` (alur host dev) tidak pernah memanggil `scripts/migrate.sh`, padahal Coolify
punya pre-deploy hook. Kini `build_and_start()` menjalankan `scripts/migrate.sh <variant>` lebih
dulu (idempotent, ledger-verified) dengan escape-hatch `SKIP_MIGRATE=1`. Service tidak bisa lagi
boot melawan schema basi.

### 4b.3 Fix #2 — `migrate.sh` memigrasi **SEMUA** tenant (bukan hanya default+dev001)
Ditemukan tenant ketiga `adatrack_gps_loadt2` tertinggal **10 migrasi** (max `017`, seharusnya
`027`). `migrate.sh` dulu hard-code `default`+`dev001`. Kini langkah `[7/8] all existing tenant
schemas` meng-enumerasi `pg_namespace` (`adatrack_gps_*` kecuali master) dan meng-apply migrasi
company ke setiap schema (idempotent, hanya schema yang sudah ada → tak mungkin "resurrect"
tenant yang di-drop). Pasca-fix: `loadt2` → **10 migrasi diterapkan**; semua tenant `applied=28`.

### 4b.4 Bukti runtime pasca-fix
| Pemeriksaan | Hasil |
|---|---|
| Ledger master / company (dev001) | `024_share_links` / `027_create_enterprise_modules` ✅ |
| Ledger per tenant | default=027 · dev001=027 · loadt2=027 (0 failure) ✅ |
| Tabel B11/B12 | `tm_protocols`, `tm_company_modules`, `tm_share_links`, `tm_heatmap_cells`, `tm_groups`, `tm_access_logs`, `tm_assets` ≠ NULL ✅ |
| `ADATRACK_IT=1 go test ./...` (api-vehicle) | **ok / 0 FAIL** (sebelumnya 10/10 FAIL) ✅ |
| `scripts/test.sh` (hermetic, 9 modul) | **0 FAIL · 14 paket `ok`** ✅ |
| Endpoint B11/B12 (17 rute) | **401** semua (terdaftar & ber-gate auth); publik `/share/:token` → 404 token tak dikenal (benar) ✅ |
| `/healthz` 7 service | **200** semua (ingestion 8090 · api-vehicle 8081 · websocket 8082 · worker-alert 8084 · worker-live 8091 · worker-persistence 8092 · service-media 8095) ✅ |
| JetStream | 7 stream (MaxAge 48 h / MaxBytes 16 GiB), 5 durable consumer ✅ |

> **Koreksi laporan sementara:** "5 endpoint B11/B12 → 404" **SALAH** — probe memakai path
> tebakan (`/enterprises`, `/share/{token}`). Path sebenarnya: `/api/v1/drivers|groups|assets|…`,
> `/api/v1/share-links`, `/api/v1/modules`, publik `/api/v1/share/:token`. Dengan path benar:
> **17/17 = 401**. Rute B11/B12 memang selalu ada di kode; 404 sebelumnya murni karena binary
> yang berjalan masih build lama (pra-redeploy).


## 4c. Harness E2E B12 + 3 bug runtime yang ditemukannya (2026-09-29)

Gap §4.6 ("belum ada `scripts/e2e-enterprise.sh`") ditutup. Harness baru
(`make e2e-enterprise`) menjalankan **HTTP nyata** terhadap PostgreSQL nyata: login →
menu/modul → CRUD enterprise (soft delete + restore) → share link (+ resolve **publik**
tanpa auth) → analytics → audit → RBAC negatif. **Hasil akhir: 31/31 PASS.**

Harness itu langsung membuktikan nilainya: ia menemukan **3 bug nyata** yang tidak
terlihat oleh unit test (fake store) maupun verifikasi §3 (SQL langsung, bukan API).

### 4c.1 `GET /audit-logs` & `POST /share-links` selalu 503 (A6)
- `ListAuditLogs`: kolom nullable (`entity_id`, `actor_email`, …) di-`Scan` ke `string`
  → `converting NULL to string is unsupported` pada baris pertama yang NULL.
- `scanShareLink`: `vehicle_ids` bertipe `bigint[]`; driver pgx mengembalikannya sebagai
  teks `{1,2}`, tidak bisa di-`Scan` ke `[]int64`.
- **Fix:** scan ke pointer + `derefString`; `vehicle_ids::text` + `parseInt64Array`.
- **Bukti:** `it_share_audit_test.go` (`TestITStoreListAuditLogs`, `TestITStoreShareLinkRoundTrip`).

### 4c.2 `vehicleStoreErr` menelan error (A7 — pelanggaran “no silent drop”)
```go
func vehicleStoreErr(err error) error { _ = err; return errUnavailable("data source unavailable") }
```
Semua kegagalan store menjadi 503 generik **tanpa jejak di log** — inilah sebabnya 503 di
atas tidak bisa didiagnosa dari log service. Kini error asli di-log (`slog.Error`), dan
langsung mengungkap A8 di bawah.

### 4c.3 `GET /api/v1/share/{token}` publik 503 (A8)
Log (setelah fix A7) menunjukkan: `store: shared vehicles: column "company_code" does not
exist (SQLSTATE 42703)`. `SharedVehicles` memfilter `WHERE company_code = $1` padahal
`tm_vehicles` **tidak punya** kolom itu pada tata letak schema-per-tenant (tenant = schema).
**Fix:** predikat dihapus (pool perusahaan sudah ter-scope schema).
**Bukti:** `TestITStoreSharedVehicles`.

> **Pelajaran (dicatat jujur):** tiga bug ini adalah **kelas yang sama** — kode jalur
> Postgres yang tidak pernah dieksekusi end-to-end. Verifikasi berbasis SQL langsung dan
> unit test fake-store **tidak** menggantikan E2E HTTP nyata. Itulah alasan harness ini
> dibuat, dan hasilnya membenarkan pembuatannya.

### 4c.4 Regression yang ditambahkan
- `scripts/e2e-enterprise.sh` + target `make e2e-enterprise` (31 check).
- `controllers/it_share_audit_test.go` (3 test IT) — jalan pada `ADATRACK_IT=1`.
- Keduanya membersihkan fixture-nya sendiri; sisa fixture dari run yang gagal sudah dibersihkan.


## 4d. Export laporan CSV (gap C3) — 2026-09-29

`GET /api/v1/reports/trips/export` dan `GET /api/v1/reports/violations/export`
mengembalikan **CSV** (`text/csv`, `Content-Disposition: attachment`) dengan data
yang **identik** dengan endpoint JSON-nya (memakai panggilan store yang sama,
sehingga kedua format tak mungkin berbeda).

- BOM UTF-8 disertakan agar Excel membuka karakter Indonesia dengan benar.
- Penulisan memakai `encoding/csv` → quoting/escaping benar (nilai ber-koma atau
  ber-tanda-kutip aman).
- Nama berkas berpola `trips_<from>_<to>.csv` (mis. `trips_20260901_20260929.csv`).

**Bukti:** `handlers_reports_export_test.go` (3 test: nama berkas, format angka,
header/attachment/BOM/quoting) dan **3 check live** di `make e2e-enterprise`
(total harness kini **38/38 PASS**).

**Sisa:** PDF dan laporan terjadwal (scheduled report) belum dibuat.

## 5. Perintah reproduksi



```bash
# unit test (tanpa infra)
cd services/api-vehicle && go build ./... && go vet ./... && go test ./... -count=1
cd internal && go test ./... -count=1      # termasuk internal/protocol + guard normalisasi

# migrasi + query terhadap PostgreSQL dev (rolled back)
set -a; . ./.env.local; set +a
export PGPASSWORD="$POSTGRES_PASSWORD" PGHOST=127.0.0.1 PGPORT=5533 \
       PGUSER="$POSTGRES_USER" PGDATABASE="$POSTGRES_DB"
psql -v ON_ERROR_STOP=1 -f /tmp/b12_verify.sql
```
