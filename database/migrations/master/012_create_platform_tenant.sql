-- ============================================================================
-- Migration: MASTER 012 — Platform Tenant (konteks primer semua company)
-- ============================================================================
-- Governance multi-tenant (PRD §6.1): penambahan company adalah operasi level
-- PLATFORM, bukan level tenant — admin sebuah company TIDAK boleh bisa membuat
-- company lain. Karena itu dibuat konteks platform yang berada DI LUAR semua
-- tenant:
--
--   1. Company registry 'DEFAULT' → database adatrack_gps_default.
--      adatrack_gps_default naik fungsi dari sekadar fallback DB (PRD §6 Key
--      Decision 6) menjadi DB konteks platform (primary all-company):
--      pool-nya otomatis ter-warm oleh TenantManager.loadCompanies() sehingga
--      login platform & routing persistence fallback keduanya berfungsi.
--   2. Role baru 'SuperAdmin' pada master.users.role (ALTER enum).
--   3. Akun platform super admin untuk dev/onboarding pertama.
--
-- Endpoint POST /api/v1/companies di-gate khusus SuperAdmin; Admin tenant
-- menerima 403 PLATFORM_ONLY. Token platform hanya boleh menyentuh endpoint
-- platform (lihat requireAuth di service-websocket / api-vehicle).
--
-- IDEMPOTEN: aman dijalankan berulang (CREATE/INSERT ... ON DUPLICATE KEY /
-- ALTER dengan definisi identik).
--
-- Kredensial DEV (GANTI di produksi!): platform@adatrackgps.local / Platform@123
-- (bcrypt cost 10, hash fixed agar konsisten antar env — pola master_seed).
-- ============================================================================

-- 1) Pastikan database konteks platform ada (idempoten; biasanya sudah dibuat
--    oleh init script via DECLARED_DBS).
CREATE DATABASE IF NOT EXISTS `adatrack_gps_default`
  CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 2) Role platform: perluas enum users.role (urutan menjaga kompatibilitas).
ALTER TABLE users MODIFY COLUMN role
  ENUM('SuperAdmin', 'Admin', 'Manager', 'Operator', 'Driver') NOT NULL;

-- 3) Registrasi tenant platform di master.companies.
--    country_code 'ID' wajib (FK countries.iso_code); settings menandai ini
--    sebagai platform scope agar mudah dibedakan saat audit/reporting.
INSERT INTO companies (code, name, legal_name, country_code, timezone,
                       settings, is_active, activated_at)
VALUES ('DEFAULT', 'adatrack Platform', 'adatrack Platform (Primary)', 'ID', 'Asia/Jakarta',
        JSON_OBJECT('scope', 'platform', 'provisionable', FALSE),
        TRUE, CURRENT_TIMESTAMP)
ON DUPLICATE KEY UPDATE
    name       = VALUES(name),
    legal_name = VALUES(legal_name),
    is_active  = TRUE,
    settings   = JSON_MERGE_PATCH(COALESCE(settings, '{}'),
                                 JSON_OBJECT('scope', 'platform', 'provisionable', FALSE));

-- 4) Akun super admin platform (id eksplisit=2 agar tidak bentrok dgn seed
--    DEF001 yang memakai 1,3,4). company_id di-resolve via subquery.
INSERT INTO users (id, company_id, company_code, email, password_hash,
                   full_name, first_name, last_name, email_verified, locale, role, status)
VALUES (2,
        (SELECT id FROM companies WHERE code = 'DEFAULT'),
        'DEFAULT',
        'platform@adatrackgps.local',
        '$2a$10$iN15lXdlTrC1M4B5AEORXeojXl8ZThEjSW9LE4j9qy2wDK1zzDLnW',
        'Platform Super Admin', 'Platform', 'SuperAdmin',
        TRUE, 'id', 'SuperAdmin', 'active')
ON DUPLICATE KEY UPDATE
    company_code  = VALUES(company_code),
    password_hash = VALUES(password_hash),
    full_name     = VALUES(full_name),
    role          = VALUES(role),
    status        = VALUES(status);

-- ============================================================================
-- End of MASTER 012
-- ============================================================================
