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
6. **E2E harness** — belum ada `scripts/e2e-enterprise.sh`; verifikasi B12 saat ini = unit test +
   SQL nyata (§3). E2E HTTP penuh (login → CRUD → share publik → audit) disarankan menyusul.

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
