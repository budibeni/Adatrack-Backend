# PostgreSQL Provider — Implementasi & Limitasi

Dokumen ini menjelaskan implementasi **PostgreSQL** sebagai penyimpanan proyek
adatrack (PRD v1.6.0 §7.1): satu physical DB dengan **schema per-tenant**,
driver `pgx`, dan konfigurasi via env `POSTGRES_*` / `DATABASE_URL`.
Sejak 2026-08-25 **postgres adalah default proyek**.

> **STATUS 2026-09-13 (PRD konsolidasi v1.6.0):** PRD §7.1 menetapkan
> **PostgreSQL sebagai satu-satunya engine persisten** (schema-per-tenant) dan
> `DATABASE_PROVIDER` tidak lagi tercantum di daftar env PRD (sitasi lama
> "PRD §7.1.1" sudah tidak ada). Dokumen ini menjadi **catatan implementasi &
> limitasi** terhadap basis kode saat ini.

## 1. Arsitektur Ringkas

| Aspek | PostgreSQL |
|---|---|
| Driver `database/sql` | `pgx` (jackc/pgx/v5/stdlib) via wrapper `pgxadatrack` |
| Multi-tenant | SATU physical DB (`POSTGRES_DB`, dev `adatrack_gps_db`) + **schema** per tenant (`adatrack_gps_master`, `adatrack_gps_<company_code>`) dipilih via `search_path` di URL DSN |
| Migration company | `database/migrations/company_pg/*.sql` (dipilih otomatis oleh `tenant.Config.MigrationsDirFor()`) |
| Menjalankan file migrasi | file di-**split per statement** oleh `internal/dialect.SplitSQLStatements()` (pgx extended protocol menolak multi-statemen) |
| Batch insert telemetry | `INSERT ... ON CONFLICT DO UPDATE SET col = EXCLUDED.col` |
| Insert → id baru | `INSERT ... RETURNING id` (`dialect.InsertReturningID`) |

`internal/dialect` adalah satu-satunya titik cabang SQL engine; kode Go service
berbagi 100% jalur.

## 2. Konfigurasi `.env`

Kunci sudah tersedia di `backend/.env` (dan `.env.example`); pastikan nilainya:

```dotenv
DATABASE_PROVIDER=postgres
POSTGRES_HOST=127.0.0.1
POSTGRES_PORT=5532                  # port HOST compose-primary (native PG pakai 5432)
POSTGRES_DB=adatrack_gps_db
POSTGRES_USER=adatrack_gps_user
POSTGRES_PASSWORD=<password>
POSTGRES_SSLMODE=disable
POSTGRES_POOL_MIN=10
POSTGRES_POOL_MAX=50
# Canonical URL (prioritas pertama dipakai internal/config):
DATABASE_URL=postgres://adatrack_gps_user:<password>@127.0.0.1:5532/adatrack_gps_db?sslmode=disable
```

Loader: service memakai `internal.LoadProjectEnv()` (walk-up ke `backend/.env`);
OS env menang atas file `.env`. Kode turunan: `config.Dialect()` /
`tenant.Config.Dialect()` dari `DATABASE_PROVIDER`.

## 3. Provisioning schema (server baru / dev)

Satu physical DB menampung semua tenant sebagai schema. Jalankan sekali
sebagai superuser/owner (idempoten — aman diulang):

```bash
psql -U postgres -c "CREATE DATABASE adatrack_gps_db OWNER adatrack_gps_user;"
psql -U adatrack_gps_user -d adatrack_gps_db -v ON_ERROR_STOP=1 -f database/init-pg/01_schemas.sql
psql -U adatrack_gps_user -d adatrack_gps_db -v ON_ERROR_STOP=1 -f database/init-pg/02_master_setup.sql
psql -U adatrack_gps_user -d adatrack_gps_db -v ON_ERROR_STOP=1 -f database/init-pg/03_company_setup.sql
```

> **Seed reference wilayah (UPDATE 2026-08-25)**: pada jalur compose, file
> `init-pg/02b-seed-reference.sh` meng-apply `seed/reference/001–005`
> (250 negara s/d 83.762 desa/kelurahan, data real BPS/mledoze/kodepos) ke
> schema master secara otomatis saat init pertama — file seed di-transpile
> on-the-fly ke sintaks PostgreSQL (`ON CONFLICT DO NOTHING`),
> satu transaksi per file, idempoten saat re-run (LIVE-verified: jumlah baris
> persis target roadmap, orphan/dup = 0). Compose service `postgres` wajib
> memuat volume `./database/seed:/db/seed:ro`. Untuk provisioning manual di
> luar compose, terapkan file yang sama dengan transform serupa.
>
> `04-create-replicator-role.sh` (otomatis via entrypoint) menyiapkan role
> `replicator` + pg_hba untuk replikasi streaming.
>
> **Docker compose**: service DB di compose adalah **PostgreSQL** (Redis &
> NATS selalu up). Jalankan via `backend/scripts/compose-up.sh up -d`.
> Workstation ini: compose primary = POSTGRES_PORT=5532 (5432 dipakai native).

## 4. Auto-provision tenant (`POST /api/v1/companies`)

`internal/tenant` dengan `provider=postgres`:
1. membuat schema `adatrack_gps_{code}` di physical DB;
2. apply `company_pg/001..012` lewat `MigrationsDirFor()` (per-statement);
3. meng-return pool (koneksi via `search_path`).

`tenant.Manager` (Master(), DB(code), DBDSN(), openPool → `cfg.DriverName()`)
berjalan penuh untuk provider PostgreSQL.

## 5. Komponen SQL-Aware (foundation)

- `internal/dialect/dialect.go` — abstraksi dialect + helper (DriverName,
  QuoteIdent, Placeholders, Upsert/ValuesExpr, InsertOrIgnore/ConflictDoNothing,
  JSON empties, ErrIsDuplicate, SplitSQLStatements) — unit test green.
- `BatchInsertDB(d, ...)` (internal database client) membaca SQL batch telemetry
  secara dialect-aware.
- `internal/config.go` — `Dialect()/DriverName()/DSN()`, `postgresDSN()`, field
  `Postgres`, `Validate()` provider-aware, `DATABASE_PROVIDER`.
- `internal/tenant/config.go` — `Provider`, `MasterDSN()/DBDSN()` provider-aware,
  `MigrationsDirFor()` (directory `*_pg`), `CompanyDBName`, pool sizing, master schema.
- `cmd/migrate-tenant` + `tenant.applyCompanyMigrations` — split per statement.

## 6. Keterbatasan (dicatat jujur)

Controllers REST (`api-vehicle`, `service-websocket`) dan `worker-alert`
`store/repos` masih memakai SQL lawas yang belum sepenuhnya PostgreSQL-native
(quoting backtick, ekspresi JSON/timestamp lama, pengambilan id tanpa
`RETURNING`, dst.).
Foundation & jalur data (config → tenant → migration → batch insert
`worker-persistence`) sudah PostgreSQL-native; **melaporkan SQL controllers
ke PostgreSQL-native penuh** adalah langkah lanjutan (fase besar, ±30 files).

**UPDATE audit 2026-08-31:** kasus kritis berikutnya ditemukan & ditutup —
lookup geofence `worker-alert` (`store_geofence.go`) memakai
`COALESCE(boundary_points, …)` dengan literal JSON kosong yang gagal TOTAL
di Postgres
(SQLSTATE 42846 json-vs-jsonb; deteksi GEOFENCE_BREACH mati diam-diam).
Kini memakai `dialect.Current().JSONArrayEmpty()` — pola yang sama dengan
fix `EnabledPreferences` B5a. Live-verified: 0 error pasca-fix. Sisanya
(±30 file SQL controllers) tetap terbuka sesuai catatan di atas.

## 7. Checklist

- [x] `internal` + 6 service `go build ./...` — green.
- [x] `internal/dialect` unit test & `internal`/`worker-persistence` tests green.
- [x] Provisioning live Postgres → master+tenant schemas, partitions telemetry, seed OK.
- [x] Compose primary PostgreSQL + helper `backend/scripts/compose-up.sh` (2026-08-25).
- [x] Seed reference wilayah via init-pg (2026-08-25): `02b-seed-reference.sh`
      transform ke `ON CONFLICT DO NOTHING`; fresh-init live verified
      250/38/514/7285/**83762** baris, integritas orphan/dup = 0, idempoten.

---
Akhir dokumen.