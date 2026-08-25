-- ============================================================================
-- Migration: MASTER 003 — provinces (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS provinces (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_id INT NOT NULL,
    code VARCHAR(10) NOT NULL,
    name VARCHAR(100) NOT NULL,
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_prov_country FOREIGN KEY (country_id) REFERENCES countries(id),
    CONSTRAINT uq_prov_country_code UNIQUE (country_id, code)
);
CREATE INDEX IF NOT EXISTS idx_prov_country ON provinces (country_id);
CREATE INDEX IF NOT EXISTS idx_prov_code ON provinces (code);
CREATE INDEX IF NOT EXISTS idx_prov_name ON provinces (name);

-- ============================================================================
-- End of MASTER 003 (postgres)
-- ============================================================================