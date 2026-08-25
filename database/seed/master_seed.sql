-- ============================================================================
-- SEED — MASTER DB (adatrack_gps_master) — dev only
-- ============================================================================
-- Data referensi + tenant dev DEV001 + vehicle_imei_map sample agar pipeline
-- B1 bisa langsung diuji setelah infra up.
-- ============================================================================

-- 1) Countries + wilayah (provinces/cities/districts/subdistricts) kini
--    di-seed otomatis oleh init script dari /db/seed/reference/*.sql
--    (dihasilkan tools/genregions — sumber data real, lihat data/raw/README.md).

-- 2) Company dev (tenant DEF001 → DB adatrack_gps_def001)
INSERT INTO companies (code, name, legal_name, country_code, address, phone, company_email, website, tax_id,
                       postal_code, timezone, settings, is_active, activated_at) VALUES
    ('DEF001', 'Development Company', 'Def Company PT', 'ID', 'Jl. Sudirman No.1, Jakarta', '+62 21 1234 5678',
     'admin@def001.io', 'https://def001.example.com', 'NPWP 01.234.567.8-901.000',
     '12190', 'Asia/Jakarta',
     JSON_OBJECT('retention_days', 90, 'max_devices', 100, 'alert_policies', JSON_OBJECT('offline_minutes', 3)),
     TRUE, CURRENT_TIMESTAMP)
ON DUPLICATE KEY UPDATE
    name = VALUES(name),
    legal_name = VALUES(legal_name),
    address = VALUES(address),
    phone = VALUES(phone),
    company_email = VALUES(company_email),
    website = VALUES(website),
    tax_id = VALUES(tax_id),
    postal_code = VALUES(postal_code),
    timezone = VALUES(timezone),
    settings = VALUES(settings),
    is_active = VALUES(is_active),
    activated_at = VALUES(activated_at);

-- 4) Sample IMEI → company mapping untuk tenant resolution (anti-spoofing).
--    vehicle_id mereferensi vehicle.id seed di adatrack_gps_def001 (company_seed.sql).
INSERT INTO vehicle_imei_map (imei, company_code, vehicle_id) VALUES
    ('864201040512345', 'DEF001', 1),
    ('864201040512346', 'DEF001', 2),
    ('864201040512347', 'DEF001', 3)
ON DUPLICATE KEY UPDATE
    company_code = VALUES(company_code),
    vehicle_id = VALUES(vehicle_id);

-- ===========================================================================
-- End of Master Seed
-- ============================================================================
-- ============================================================================
-- SEED B2 (appended) — DEF users multi-tenant untuk login E2E service-websocket
-- Password: admin@def001.io / Admin@123, operator@def001.io / Operator@123,
-- driver@def001.io / Driver@123 (bcrypt hash FIXED agar konsisten semua env)
-- ============================================================================
-- B2 seed: users DEF001 (master, GLOBAL auth authority)
-- company_id di-resolve via subquery agar selalu valid terhadap FK
-- users.company_id → companies.id (tidak bergantung pada AUTO_INCREMENT).
INSERT INTO users (id, company_id, company_code, email, password_hash, full_name, first_name, last_name, role,
                    status, email_verified, mfa_enabled, locale) VALUES
    (1, (SELECT id FROM companies WHERE code = 'DEF001'), 'DEF001', 'admin@def001.io', '$2a$10$lhkXH5AoKpHwAQPiOzmpHupMKey.r0TIx85caonzT5pwlyisxeW/m', 'Admin Default', 'Admin', 'Dev', 'Admin',
     'active', TRUE, FALSE, 'id'),
    (3, (SELECT id FROM companies WHERE code = 'DEF001'), 'DEF001', 'operator@def001.io', '$2a$10$.8kf8iIZUy/HdRIz/.HTaeTl1OIdWoPZdd0R2/QQjX0l.vRuhLoZK', 'Operator Default', 'Operator', 'Dev', 'Operator',
     'active', TRUE, FALSE, 'id'),
    (4, (SELECT id FROM companies WHERE code = 'DEF001'), 'DEF001', 'driver@def001.io', '$2a$10$qPuYEJEP60l9GotE2qepkODGAsA89C7GtbxG4TRePzOgl//6/q3d2', 'Driver Default', 'Driver', 'Dev', 'Driver',
     'active', TRUE, FALSE, 'id')
ON DUPLICATE KEY UPDATE
    password_hash       = VALUES(password_hash),
    full_name           = VALUES(full_name),
    first_name           = VALUES(first_name),
    last_name            = VALUES(last_name),
    role                 = VALUES(role),
    status               = VALUES(status),
    email_verified       = VALUES(email_verified),
    mfa_enabled          = VALUES(mfa_enabled),
    locale               = VALUES(locale);

-- Pastikan password admin def konsisten (hash FIXED untuk semua env).
UPDATE users SET password_hash = '$2a$10$lhkXH5AoKpHwAQPiOzmpHupMKey.r0TIx85caonzT5pwlyisxeW/m' WHERE email = 'admin@def001.io';
