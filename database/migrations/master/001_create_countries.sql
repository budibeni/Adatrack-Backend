-- ============================================================================
-- Migration: MASTER 001 — countries
-- ============================================================================
-- Master reference data: ISO 3166-1 countries (PRD §6.1).
-- Dipakai untuk company registration + multi-country operations.
-- Harus dibuat duluan karena di-referensikan oleh companies.country_code.
-- ============================================================================

CREATE TABLE IF NOT EXISTS countries (
    id INT AUTO_INCREMENT PRIMARY KEY,
    iso_code VARCHAR(2) UNIQUE NOT NULL,            -- ISO 3166-1 alpha-2, e.g. "ID", "MY", "US"
    iso_code_3 VARCHAR(3) UNIQUE,                   -- ISO 3166-1 alpha-3, e.g. "IDN", "MYS"
    name VARCHAR(100) NOT NULL,
    phone_code VARCHAR(10),                         -- e.g. "+62", "+1", "+44"
    currency_code VARCHAR(3),                       -- ISO 4217, e.g. "IDR", "USD", "MYR"
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_iso_code (iso_code),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 001
-- ============================================================================