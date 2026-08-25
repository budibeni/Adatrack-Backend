-- ============================================================================
-- Migration: MASTER 006 — subdistricts (desa / kelurahan, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS subdistricts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    district_id BIGINT NOT NULL,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10),
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_sub_district FOREIGN KEY (district_id) REFERENCES districts(id),
    CONSTRAINT uq_sub_code UNIQUE (code)
);
CREATE INDEX IF NOT EXISTS idx_sub_district ON subdistricts (district_id);
CREATE INDEX IF NOT EXISTS idx_sub_code ON subdistricts (code);
CREATE INDEX IF NOT EXISTS idx_sub_name ON subdistricts (name);
CREATE INDEX IF NOT EXISTS idx_sub_postal ON subdistricts (postal_code);

-- ============================================================================
-- End of MASTER 006 (postgres)
-- ============================================================================