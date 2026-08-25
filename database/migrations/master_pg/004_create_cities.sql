-- ============================================================================
-- Migration: MASTER 004 — cities (kabupaten / kota, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS cities (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_id INT NOT NULL,
    province_id BIGINT,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_city_country FOREIGN KEY (country_id) REFERENCES countries(id),
    CONSTRAINT fk_city_province FOREIGN KEY (province_id) REFERENCES provinces(id),
    CONSTRAINT uq_city_code UNIQUE (code)
);
CREATE INDEX IF NOT EXISTS idx_city_country ON cities (country_id);
CREATE INDEX IF NOT EXISTS idx_city_province ON cities (province_id);
CREATE INDEX IF NOT EXISTS idx_city_code ON cities (code);
CREATE INDEX IF NOT EXISTS idx_city_name ON cities (name);

-- ============================================================================
-- End of MASTER 004 (postgres)
-- ============================================================================