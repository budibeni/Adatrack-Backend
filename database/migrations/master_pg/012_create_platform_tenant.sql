-- ============================================================================
-- Migration: MASTER 012 — Platform Tenant (PostgreSQL)
-- ============================================================================
-- Governance multi-tenant (PRD §6.1): konteks platform 'DEFAULT' + akun
-- SuperAdmin. Varian PostgreSQL:
--   * TIDAK ada CREATE DATABASE (arsitektur PG = satu DB, tenant = SCHEMA).
--     Schema adatrack_gps_master dibuat oleh init-pg/01_schemas.sql.
--   * users.role CHECK sudah memuat 'SuperAdmin' (lihat 007) — tanpa ALTER.
--   * UPSERT memakai ON CONFLICT (code)/(email) — bukan ON DUPLICATE KEY.
--
-- Kredensial DEV (GANTI di produksi!): platform@adatrackgps.local / Platform@123
-- (bcrypt, konsisten dengan master_seed MySQL).
-- ============================================================================

-- 1) Registrasi tenant platform di master.companies.
INSERT INTO companies (code, name, legal_name, country_code, timezone,
                       settings, is_active, activated_at)
VALUES ('DEFAULT', 'adatrack Platform', 'adatrack Platform (Primary)', 'ID', 'Asia/Jakarta',
        '{"scope":"platform","provisionable":false}'::jsonb,
        TRUE, CURRENT_TIMESTAMP)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    legal_name = EXCLUDED.legal_name,
    is_active = TRUE,
    settings = EXCLUDED.settings;

-- 2) Akun super admin platform (id eksplisit 2 agar tidak bentrok dengan seed
--    DEF001 yang memakai 1,3,4).
INSERT INTO users (id, company_id, company_code, email, password_hash,
                   full_name, first_name, last_name, email_verified, locale, role, status)
VALUES (2,
        (SELECT id FROM companies WHERE code = 'DEFAULT'),
        'DEFAULT',
        'platform@adatrackgps.local',
        '$2a$10$iN15lXdlTrC1M4B5AEORXeojXl8ZThEjSW9LE4j9qy2wDK1zzDLnW',
        'Platform Super Admin', 'Platform', 'SuperAdmin',
        TRUE, 'id', 'SuperAdmin', 'active')
ON CONFLICT (id) DO UPDATE SET
    company_code = EXCLUDED.company_code,
    password_hash = EXCLUDED.password_hash,
    full_name = EXCLUDED.full_name,
    role = EXCLUDED.role,
    status = EXCLUDED.status;

-- ============================================================================
-- End of MASTER 012 (postgres)
-- ============================================================================