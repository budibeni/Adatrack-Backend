-- ============================================================================
-- Migration: MASTER 006 — tm_districts (kecamatan, BPS)
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_districts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    city_id BIGINT NOT NULL,
    code VARCHAR(15) NOT NULL,
    name VARCHAR(100) NOT NULL,
    postal_code VARCHAR(10),
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_districts_code UNIQUE (code),
    CONSTRAINT fk_tm_districts_city FOREIGN KEY (city_id) REFERENCES tm_cities (id)
);
CREATE INDEX IF NOT EXISTS idx_tm_districts_city ON tm_districts (city_id);
CREATE INDEX IF NOT EXISTS idx_tm_districts_name ON tm_districts (name);
CREATE INDEX IF NOT EXISTS idx_tm_districts_deleted ON tm_districts (deleted_at);