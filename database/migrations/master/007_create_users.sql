-- ============================================================================
-- Migration: MASTER 006 — users (GLOBAL Auth Authority)
-- ============================================================================
-- Password hash disimpan sekali di sini. Company DB hanya referensi user_id
-- via user_company_access (tabel lokal di company DB).
-- Login flow: email → company_code + password verify di master.users (PRD §6.1).
--
-- Enterprise-standard identity fields (SCIM / SaaS-IAM aligned):
--   username             — System login name (SCIM userName); unique
--   first_name/last_name — Structured name (SCIM name.givenName/familyName);
--                          full_name tetap disimpan untuk backward-compat
--   phone_number         — Phone number E.164
--   email_verified / phone_verified — Flag verifikasi kontak
--   mfa_enabled          — Multi-factor authentication flag
--   password_changed_at  — Timestamp perubahan password terakhir
--   failed_login_attempts / locked_until — Account lockout (GAP #12)
--   locale               — Preferensi bahasa user untuk i18n (default 'id')
--   avatar_url           — URL foto profil
--   created_by/updated_by — Audit trail (FK logis ke users.id)
--   deleted_at           — Soft delete (NULL = aktif)
-- ============================================================================

CREATE TABLE IF NOT EXISTS users (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    company_id BIGINT NOT NULL,                     -- FK ke companies.id
    company_code VARCHAR(20) NOT NULL,              -- denormalisasi kecepatan lookup
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,            -- bcrypt cost 12
    full_name VARCHAR(100) NOT NULL,

    -- --- Enterprise-standard identity fields ---
    username VARCHAR(191) UNIQUE NULL,              -- SCIM userName; NULL = email-only login
    first_name VARCHAR(100) NULL,                   -- given name
    last_name VARCHAR(100) NULL,                    -- family name
    phone_number VARCHAR(20) NULL,                  -- format E.164, e.g. "+6281234567890"
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,  -- flag verifikasi email
    phone_verified BOOLEAN NOT NULL DEFAULT FALSE,  -- flag verifikasi telepon
    mfa_enabled BOOLEAN NOT NULL DEFAULT FALSE,     -- multi-factor authentication
    password_changed_at TIMESTAMP NULL DEFAULT NULL,
    failed_login_attempts INT NOT NULL DEFAULT 0,   -- counter lockout
    locked_until TIMESTAMP NULL DEFAULT NULL,       -- account lockout expiry
    locale VARCHAR(10) NOT NULL DEFAULT 'id',       -- preferensi locale i18n
    avatar_url VARCHAR(512) NULL,                   -- foto profil

    role ENUM('Admin', 'Manager', 'Operator', 'Driver') NOT NULL,
    status ENUM('active', 'inactive', 'suspended') NOT NULL DEFAULT 'active',
    last_login TIMESTAMP NULL,

    -- --- Audit & soft delete (enterprise-standard) ---
    created_by BIGINT NULL DEFAULT NULL,            -- audit: users.id pembuat
    updated_by BIGINT NULL DEFAULT NULL,            -- audit: users.id pengubah
    deleted_at TIMESTAMP NULL DEFAULT NULL,         -- soft delete (NULL = aktif)

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (company_id) REFERENCES companies(id),
    INDEX idx_company (company_id),
    INDEX idx_company_code (company_code),
    INDEX idx_email (email),
    INDEX idx_username (username),
    INDEX idx_role (role),
    INDEX idx_locked (locked_until),
    INDEX idx_locale (locale),
    INDEX idx_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 006
-- ===========================================================================
