-- ============================================================================
-- PostgreSQL bootstrap 02 — master setup (PRD §6.1, §7.1, §14.5)
-- ============================================================================
-- Executed by the docker entrypoint (docker-entrypoint-initdb.d) right after
-- 01_schemas.sql. It is SELF-SUFFICIENT: it applies every master migration with
-- `\i`, then seeds the platform/dev tenants, dev users and the IMEI allowlist.
--
-- scripts/migrate.sh (the ledger-based path used by Coolify pre-deploy and by
-- service boot) applies the same migrations with checksum + ledger FIRST and
-- then runs this file: every statement below is idempotent, so both orders are
-- safe and a double run never errors (acceptance: "init-pg idempoten").
-- ============================================================================

-- Path/variable setup -------------------------------------------------------
-- migrations_dir defaults to the container mount (/db/migrations) so the docker
-- entrypoint can run this file as-is.
--   * Docker entrypoint : no -v → default path, migrations applied here.
--   * scripts/migrate.sh: passes -v migrations_dir=<repo>/database/migrations
--     and -v skip_migrations=1 because the ledger-based migrator already applied
--     them (single source of truth for the ledger + timing).
\if :{?migrations_dir}
\else
\set migrations_dir /db/migrations
\endif

SET search_path TO adatrack_gps_master;

\if :{?skip_migrations}
\echo '02_master_setup: master migrations already applied by the caller — skipping \i block'
\else
\i :migrations_dir/master_pg/001_create_schema_migrations.sql
\i :migrations_dir/master_pg/002_create_countries.sql
\i :migrations_dir/master_pg/003_create_companies.sql
\i :migrations_dir/master_pg/004_create_provinces.sql
\i :migrations_dir/master_pg/005_create_cities.sql
\i :migrations_dir/master_pg/006_create_districts.sql
\i :migrations_dir/master_pg/007_create_subdistricts.sql
\i :migrations_dir/master_pg/008_create_users.sql
\i :migrations_dir/master_pg/009_create_users_b2c.sql
\i :migrations_dir/master_pg/010_create_vehicle_imei_map.sql
\i :migrations_dir/master_pg/011_create_vehicle_categories.sql
\i :migrations_dir/master_pg/012_create_vehicle_types.sql
\i :migrations_dir/master_pg/013_create_audit_logs.sql
\i :migrations_dir/master_pg/014_create_modules.sql
\i :migrations_dir/master_pg/015_create_menus.sql
\i :migrations_dir/master_pg/016_seed_modules_menus.sql
\i :migrations_dir/master_pg/017_create_platform_tenant.sql
\i :migrations_dir/master_pg/018_create_company_media_config.sql
\i :migrations_dir/master_pg/019_create_platform_admin.sql
\i :migrations_dir/master_pg/020_repair_platform_admin.sql
\endif

-- -- -- Minimal country seed required by tm_companies.country_code FK -- -- --
INSERT INTO tm_countries (iso_code, iso_code_3, name, phone_code, currency_code, is_active)
VALUES ('ID', 'IDN', 'Indonesia', '+62', 'IDR', TRUE)
ON CONFLICT (iso_code) DO UPDATE SET
    iso_code_3 = EXCLUDED.iso_code_3,
    name = EXCLUDED.name,
    phone_code = EXCLUDED.phone_code,
    currency_code = EXCLUDED.currency_code;

-- -- -- Dev tenant DEV001 (staging fixtures; harmless in production) -- -- --
INSERT INTO tm_companies (code, name, legal_name, country_code, address, phone, company_email, website,
                          tax_id, postal_code, timezone, business_type, settings, is_active, activated_at)
VALUES ('DEV001', 'Development Company', 'Dev Company PT', 'ID', 'Jl. Sudirman No.1, Jakarta',
        '+62 21 1234 5678', 'admin@dev001.io', 'https://dev001.example.com', 'NPWP 01.234.567.8-901.000',
        '12190', 'Asia/Jakarta', 'b2b',
        '{"retention_days":90,"max_devices":100,"alert_policies":{"offline_minutes":3}}'::jsonb,
        TRUE, CURRENT_TIMESTAMP)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    legal_name = EXCLUDED.legal_name,
    address = EXCLUDED.address,
    phone = EXCLUDED.phone,
    company_email = EXCLUDED.company_email,
    website = EXCLUDED.website,
    tax_id = EXCLUDED.tax_id,
    postal_code = EXCLUDED.postal_code,
    timezone = EXCLUDED.timezone,
    business_type = EXCLUDED.business_type,
    settings = EXCLUDED.settings;

-- -- -- Dev users (password: Admin@123, bcrypt cost 12; MUST be rotated) -- -- --
-- The bcrypt hash below is a REAL cost-12 hash of `Admin@123` (verified), so the
-- dev accounts are usable in B2 login tests. Production tenant admins are created
-- by FR-5.5 auto-provisioning with must_change_password = TRUE.
--
-- IDS MUST NEVER BE HARDCODED HERE. `tm_users` is shared with the platform
-- SuperAdmin (master 019), so a fixed `id = 1` silently collides with that row:
-- the previous `ON CONFLICT (id) DO UPDATE` rewrote the platform identity into a
-- DEV001 `Admin` (privilege escalation) and left `admin@dev001.io` missing
-- entirely. `uq_tm_users_email` makes the email the stable natural key — the
-- conflict target, so re-running this file only ever touches these three rows.
INSERT INTO tm_users (company_id, company_code, email, password_hash, full_name, first_name, last_name,
                      global_role, is_active, email_verified, mfa_enabled, locale)
VALUES
    ((SELECT id FROM tm_companies WHERE code = 'DEV001'), 'DEV001', 'admin@dev001.io',
     '$2a$12$68Y3c8vQndkODvLUKj52RuC02x8yLdpJorBYysKXAkLvV9mgf3YTK', 'Admin Default', 'Admin', 'Dev',
     'Admin', TRUE, TRUE, FALSE, 'id'),
    ((SELECT id FROM tm_companies WHERE code = 'DEV001'), 'DEV001', 'operator@dev001.io',
     '$2a$12$68Y3c8vQndkODvLUKj52RuC02x8yLdpJorBYysKXAkLvV9mgf3YTK', 'Operator Default', 'Operator', 'Dev',
     'Operator', TRUE, TRUE, FALSE, 'id'),
    ((SELECT id FROM tm_companies WHERE code = 'DEV001'), 'DEV001', 'driver@dev001.io',
     '$2a$12$68Y3c8vQndkODvLUKj52RuC02x8yLdpJorBYysKXAkLvV9mgf3YTK', 'Driver Default', 'Driver', 'Dev',
     'Driver', TRUE, TRUE, FALSE, 'id')
ON CONFLICT (email) DO UPDATE SET
    company_id = EXCLUDED.company_id,
    company_code = EXCLUDED.company_code,
    password_hash = EXCLUDED.password_hash,
    full_name = EXCLUDED.full_name,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    global_role = EXCLUDED.global_role,
    is_active = EXCLUDED.is_active,
    email_verified = EXCLUDED.email_verified;

SELECT setval(pg_get_serial_sequence('tm_users', 'id'),
              GREATEST((SELECT COALESCE(MAX(id), 1) FROM tm_users), 4));

-- -- -- IMEI allowlist (anti-spoofing tenant resolution, FR-1.4) -- -- --
INSERT INTO tm_vehicle_imei_map (imei, company_code, vehicle_id, is_active) VALUES
    ('864201040512345', 'DEV001', 1, TRUE),
    ('864201040512346', 'DEV001', 2, TRUE),
    ('864201040512347', 'DEV001', 3, TRUE)
ON CONFLICT (imei) DO UPDATE SET
    company_code = EXCLUDED.company_code,
    vehicle_id = EXCLUDED.vehicle_id,
    is_active = EXCLUDED.is_active;

-- -- -- Media config for the dev tenant (consumed by B5b) -- -- --
INSERT INTO tm_company_media_config (company_code, bucket, retention_days, max_file_mb, hmac_secret)
VALUES ('DEV001', 'adatrack-media', 30, 100, 'dev001-hmac-secret-b5b')
ON CONFLICT (company_code) DO UPDATE SET
    bucket = EXCLUDED.bucket,
    retention_days = EXCLUDED.retention_days,
    max_file_mb = EXCLUDED.max_file_mb,
    hmac_secret = EXCLUDED.hmac_secret;