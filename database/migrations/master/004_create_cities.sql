-- ============================================================================
-- Migration: MASTER 004 — cities (kabupaten / kota)
-- ----------------------------------------------------------------------------
-- Referensi kota tingkat-2 (kabupaten/kota). code memakai kode BPS 4-digit
-- dengan prefix provinsi (mis. '11.01' Kabupaten Aceh Selatan).
-- province_id FK → provinces(id); dibuat NULL agar kota negara tanpa provinsi
-- tetap bisa direpresentasikan. lat/lng = centroid (diisi lewat seed).
-- ============================================================================

CREATE TABLE IF NOT EXISTS cities (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    country_id INT NOT NULL,                                         -- denormalisasi cepat ke negara
    province_id BIGINT NULL,                                         -- NULL bila tak ada provinsi
    code VARCHAR(20) NOT NULL,                                       -- BPS kab/kota, unik global
    name VARCHAR(100) NOT NULL,
    latitude DECIMAL(10, 8) NULL,
    longitude DECIMAL(11, 8) NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    FOREIGN KEY (country_id) REFERENCES countries(id),
    FOREIGN KEY (province_id) REFERENCES provinces(id),
    UNIQUE KEY uk_code (code),
    INDEX idx_country (country_id),
    INDEX idx_province (province_id),
    INDEX idx_code (code),
    INDEX idx_name (name)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 004
-- ============================================================================