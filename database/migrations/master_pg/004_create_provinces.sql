-- ============================================================================
-- Migration: MASTER 004 — tm_provinces (provinsi, BPS Kemendagri)
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_provinces (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    country_id BIGINT NOT NULL,
    code VARCHAR(10) NOT NULL,
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
    CONSTRAINT uq_tm_provinces_code UNIQUE (code),
    CONSTRAINT fk_tm_provinces_country FOREIGN KEY (country_id) REFERENCES tm_countries (id)
);
CREATE INDEX IF NOT EXISTS idx_tm_provinces_country ON tm_provinces (country_id);
CREATE INDEX IF NOT EXISTS idx_tm_provinces_name ON tm_provinces (name);
CREATE INDEX IF NOT EXISTS idx_tm_provinces_deleted ON tm_provinces (deleted_at);