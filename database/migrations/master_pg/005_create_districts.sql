-- ============================================================================
-- Migration: MASTER 005 — districts (kecamatan, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS districts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    city_id BIGINT NOT NULL,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10),
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_dist_city FOREIGN KEY (city_id) REFERENCES cities(id),
    CONSTRAINT uq_dist_code UNIQUE (code)
);
CREATE INDEX IF NOT EXISTS idx_dist_city ON districts (city_id);
CREATE INDEX IF NOT EXISTS idx_dist_code ON districts (code);
CREATE INDEX IF NOT EXISTS idx_dist_name ON districts (name);
CREATE INDEX IF NOT EXISTS idx_dist_postal ON districts (postal_code);

-- ============================================================================
-- End of MASTER 005 (postgres)
-- ============================================================================