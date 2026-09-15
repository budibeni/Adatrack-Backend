-- ============================================================================
-- Migration: MASTER 005 — tm_cities (kabupaten/kota, BPS)
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_cities (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_id BIGINT NOT NULL,
    province_id BIGINT,
    code VARCHAR(15) NOT NULL,
    name VARCHAR(100) NOT NULL,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_cities_code UNIQUE (code),
    CONSTRAINT fk_tm_cities_country FOREIGN KEY (country_id) REFERENCES tm_countries (id),
    CONSTRAINT fk_tm_cities_province FOREIGN KEY (province_id) REFERENCES tm_provinces (id)
);
CREATE INDEX IF NOT EXISTS idx_tm_cities_country ON tm_cities (country_id);
CREATE INDEX IF NOT EXISTS idx_tm_cities_province ON tm_cities (province_id);
CREATE INDEX IF NOT EXISTS idx_tm_cities_name ON tm_cities (name);
CREATE INDEX IF NOT EXISTS idx_tm_cities_deleted ON tm_cities (deleted_at);