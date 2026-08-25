-- ============================================================================
-- Migration: MASTER 003 — provinces
-- ----------------------------------------------------------------------------
-- Master reference: provinsi/negara-bagian level-1. Di-Indonesia memakai kode
-- BPS/Kemendagri 2-digit (mis. '11' Aceh, '31' DKI Jakarta, '94' Papua Tengah).
-- country_id FK ke countries(iso_code) agar multi-negara tetap terjaga
-- (province NULL bila negara tak ada provinsi, mis. Singapura).
-- lat/lng = centroid (diisi lewat seed; nullable untuk negara lain).
-- ============================================================================

CREATE TABLE IF NOT EXISTS provinces (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    country_id INT NOT NULL,
    code VARCHAR(10) NOT NULL,                                      -- ISO/BPS 2-digit, unik per-negara
    name VARCHAR(100) NOT NULL,
    latitude DECIMAL(10, 8) NULL,
    longitude DECIMAL(11, 8) NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (country_id) REFERENCES countries(id),
    UNIQUE KEY uk_country_code (country_id, code),                  -- kode unik per negara
    INDEX idx_country (country_id),
    INDEX idx_code (code),
    INDEX idx_name (name)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 003
-- ============================================================================