-- ============================================================================
-- Migration: MASTER 006 — subdistricts (desa / kelurahan)
-- ----------------------------------------------------------------------------
-- Referensi kelurahan/desa tingkat-4 (Di Indonesia: kelurahan/desa).
-- code memakai kode BPS 8-digit (mis. '11.01.01.2001').
-- postal_code + lat/lng nullable — diisi best-effort lewat seed (kode pos
-- resminya ada di level kelurahan; lat/lng desa tidak tersedia di semua sumber).
-- ============================================================================

CREATE TABLE IF NOT EXISTS subdistricts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    district_id BIGINT NOT NULL,
    code VARCHAR(20) NOT NULL,                                        -- BPS desa 8-digit, unik global
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10) NULL,                                     -- kodepos (jika tersedia)
    latitude DECIMAL(10, 8) NULL,
    longitude DECIMAL(11, 8) NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (district_id) REFERENCES districts(id),
    UNIQUE KEY uk_code (code),
    INDEX idx_district (district_id),
    INDEX idx_code (code),
    INDEX idx_name (name),
    INDEX idx_postal (postal_code)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 006
-- ============================================================================