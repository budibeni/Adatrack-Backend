-- ============================================================================
-- PostgreSQL init (02): Master setup — apply master_pg migrations + dev seed
-- ============================================================================
-- Dipasang ke docker-entrypoint-initdb.d setelah 01_schemas.sql; dieksekusi
-- oleh psql sebagai POSTGRES_USER terhadap POSTGRES_DB. Varian MySQL
-- (00-multitenant-init.sh) TIDAK dihapus — skrip ini khusus provider postgres.
--
-- Struktur: SET search_path ke schema master, lalu `\i` tiap file migrasi
-- master_pg (psql meta-command; jalur absolut karena volume migrations
-- di-<mapping> ke /db/migrations).
-- ============================================================================

SET search_path TO adatrack_gps_master;

\i /db/migrations/master_pg/001_create_countries.sql
\i /db/migrations/master_pg/002_create_companies.sql
\i /db/migrations/master_pg/003_create_provinces.sql
\i /db/migrations/master_pg/004_create_cities.sql
\i /db/migrations/master_pg/005_create_districts.sql
\i /db/migrations/master_pg/006_create_subdistricts.sql
\i /db/migrations/master_pg/007_create_users.sql
\i /db/migrations/master_pg/008_create_vehicle_imei_map.sql
\i /db/migrations/master_pg/009_create_vehicle_categories.sql
\i /db/migrations/master_pg/010_create_vehicle_types.sql
\i /db/migrations/master_pg/011_create_audit_logs.sql

-- -- -- Seed minimal negara (dibutuhkan FK companies.country_code & DEFAULT) -- --
INSERT INTO countries (iso_code, iso_code_3, name, phone_code, currency_code, is_active) VALUES
    ('ID', 'IDN', 'Indonesia', '+62', 'IDR', TRUE)
ON CONFLICT (iso_code) DO UPDATE SET
    iso_code_3 = EXCLUDED.iso_code_3,
    name = EXCLUDED.name,
    phone_code = EXCLUDED.phone_code,
    currency_code = EXCLUDED.currency_code;

-- -- -- Tenant platform (012) -- --
\i /db/migrations/master_pg/012_create_platform_tenant.sql

-- -- -- Media config (B5b, Modul 8): master migration 013 + seed dev DEV001 -- --
\i /db/migrations/master_pg/013_create_company_media_config.sql
INSERT INTO company_media_config (company_code, bucket, retention_days, max_file_mb, hmac_secret)
VALUES ('DEV001', 'adatrack-media', 30, 100, 'dev001-hmac-secret-b5b')
ON CONFLICT (company_code) DO UPDATE SET
    bucket = EXCLUDED.bucket,
    retention_days = EXCLUDED.retention_days,
    max_file_mb = EXCLUDED.max_file_mb,
    hmac_secret = EXCLUDED.hmac_secret;

-- -- -- Seed dev tenant DEV001 (mirror master_seed.sql MySQL) -- --
INSERT INTO companies (code, name, legal_name, country_code, address, phone, company_email, website, tax_id,
                       postal_code, timezone, settings, is_active, activated_at)
VALUES ('DEV001', 'Development Company', 'Dev Company PT', 'ID', 'Jl. Sudirman No.1, Jakarta', '+62 21 1234 5678',
        'admin@dev001.io', 'https://dev001.example.com', 'NPWP 01.234.567.8-901.000',
        '12190', 'Asia/Jakarta',
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
    settings = EXCLUDED.settings;

INSERT INTO users (id, company_id, company_code, email, password_hash, full_name, first_name, last_name, role,
                   status, email_verified, mfa_enabled, locale)
VALUES
    (1, (SELECT id FROM companies WHERE code = 'DEV001'), 'DEV001', 'admin@dev001.io', '$2a$10$lhkXH5AoKpHwAQPiOzmpHupMKey.r0TIx85caonzT5pwlyisxeW/m', 'Admin Default', 'Admin', 'Dev', 'Admin', 'active', TRUE, FALSE, 'id'),
    (3, (SELECT id FROM companies WHERE code = 'DEV001'), 'DEV001', 'operator@dev001.io', '$2a$10$.8kf8iIZUy/HdRIz/.HTaeTl1OIdWoPZdd0R2/QQjX0l.vRuhLoZK', 'Operator Default', 'Operator', 'Dev', 'Operator', 'active', TRUE, FALSE, 'id'),
    (4, (SELECT id FROM companies WHERE code = 'DEV001'), 'DEV001', 'driver@dev001.io', '$2a$10$qPuYEJEP60l9GotE2qepkODGAsA89C7GtbxG4TRePzOgl//6/q3d2', 'Driver Default', 'Driver', 'Dev', 'Driver', 'active', TRUE, FALSE, 'id')
ON CONFLICT (id) DO UPDATE SET
    password_hash = EXCLUDED.password_hash,
    full_name = EXCLUDED.full_name,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    role = EXCLUDED.role,
    status = EXCLUDED.status,
    email_verified = EXCLUDED.email_verified;

-- Pastikan urutan identity mengikuti id tertinggi (by-default identity).
SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST((SELECT COALESCE(MAX(id),1) FROM users), 4));

-- Sample IMEI → company mapping (anti-spoofing tenant resolution)
INSERT INTO vehicle_imei_map (imei, company_code, vehicle_id) VALUES
    ('864201040512345', 'DEV001', 1),
    ('864201040512346', 'DEV001', 2),
    ('864201040512347', 'DEV001', 3)
ON CONFLICT (imei) DO UPDATE SET
    company_code = EXCLUDED.company_code,
    vehicle_id = EXCLUDED.vehicle_id;