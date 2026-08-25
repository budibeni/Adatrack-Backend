-- ============================================================================
-- Migration: MASTER 005 — districts (kecamatan)
-- ----------------------------------------------------------------------------
-- Referensi kecamatan tingkat-3. code memakai kode BPS 6-digit
-- (mis. '11.01.01'). postal_code + lat/lng nullable (diisi best-effort lewat
-- seed; postal_code resminya berada di level kelurahan/desa).
-- ============================================================================

CREATE TABLE IF NOT EXISTS districts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    city_id BIGINT NOT NULL,
    code VARCHAR(20) NOT NULL,                                        -- BPS kecamatan 6-digit, unik global
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10) NULL,
    latitude DECIMAL(10, 8) NULL,                                     -- best-effort (seed; sering NULL)
    longitude DECIMAL(11, 8) NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (city_id) REFERENCES cities(id),
    UNIQUE KEY uk_code (code),
    INDEX idx_city (city_id),
    INDEX idx_code (code),
    INDEX idx_name (name),
    INDEX idx_postal (postal_code)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 005
-- ============================================================================