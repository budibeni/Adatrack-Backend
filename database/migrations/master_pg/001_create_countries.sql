-- ============================================================================
-- Migration: MASTER 001 — countries (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS countries (
    id INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    iso_code VARCHAR(2) NOT NULL,
    iso_code_3 VARCHAR(3),
    name VARCHAR(100) NOT NULL,
    phone_code VARCHAR(10),
    currency_code VARCHAR(3),
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_countries_iso UNIQUE (iso_code),
    CONSTRAINT uq_countries_iso3 UNIQUE (iso_code_3)
);
CREATE INDEX IF NOT EXISTS idx_cty_iso ON countries (iso_code);
CREATE INDEX IF NOT EXISTS idx_cty_active ON countries (is_active);

-- ============================================================================
-- End of MASTER 001 (postgres)
-- ============================================================================