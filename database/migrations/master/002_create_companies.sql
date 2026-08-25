-- ============================================================================
-- Migration: MASTER 002 — companies (Tenant Registry)
-- ============================================================================
-- Master DB. Setiap company = satu tenant → satu database adatrack_gps_{code}.
-- country_code FK ke countries.iso_code (ISO 3166-1 alpha-2).
--
-- Enterprise-standard tenant registry fields:
--   legal_name            — Nama badan hukum (boleh berbeda dari display name)
--   company_email         — Email kontak resmi perusahaan
--   website               — URL situs web perusahaan
--   tax_id                — Nomor identitas pajak (NPWP, VAT, dll.)
--   postal_code           — Kode pos alamat terdaftar
--   activated_at          — Timestamp aktivasi akun tenant
--   created_by/updated_by — Audit trail (FK logis ke users.id)
--   deleted_at            — Soft delete (NULL = aktif)
-- ============================================================================

CREATE TABLE IF NOT EXISTS companies (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    code VARCHAR(20) UNIQUE NOT NULL,               -- e.g. "ABLE01", "LOGI002"
    name VARCHAR(100) NOT NULL,

    -- --- Enterprise-standard tenant registry fields ---
    legal_name VARCHAR(255) NULL,                   -- nama badan hukum
    company_email VARCHAR(255) NULL,                -- email kontak resmi
    website VARCHAR(255) NULL,                      -- URL situs web
    tax_id VARCHAR(50) NULL,                        -- NPWP / VAT number
    postal_code VARCHAR(10) NULL,                   -- kode pos alamat

    country_code VARCHAR(2) NOT NULL,               -- ISO 3166-1 alpha-2, e.g. "ID", "MY", "US"
    address TEXT,                                   -- perusahaan physical address
    phone VARCHAR(20),                              -- kontak telepon perusahaan
    timezone VARCHAR(50) DEFAULT 'Asia/Jakarta',    -- IANA timezone, e.g. "Asia/Jakarta"
    settings JSON,                                  -- retention_policy, max_devices, dll.
    is_active BOOLEAN DEFAULT TRUE,
    activated_at TIMESTAMP NULL DEFAULT NULL,       -- timestamp aktivasi tenant

    -- --- Audit & soft delete (enterprise-standard) ---
    created_by BIGINT NULL DEFAULT NULL,            -- audit: users.id pembuat
    updated_by BIGINT NULL DEFAULT NULL,            -- audit: users.id pengubah
    deleted_at TIMESTAMP NULL DEFAULT NULL,         -- soft delete (NULL = aktif)

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (country_code) REFERENCES countries(iso_code),
    INDEX idx_code (code),
    INDEX idx_country (country_code),
    INDEX idx_active (is_active),
    INDEX idx_tax_id (tax_id),
    INDEX idx_deleted (deleted_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 002
-- ===========================================================================
