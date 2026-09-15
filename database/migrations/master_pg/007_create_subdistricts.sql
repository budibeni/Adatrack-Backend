-- ============================================================================
-- Migration: MASTER 007 — tm_subdistricts (desa/kelurahan, BPS + kodepos)
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_subdistricts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    district_id BIGINT NOT NULL,
    code VARCHAR(20) NOT NULL,
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
    CONSTRAINT uq_tm_subdistricts_code UNIQUE (code),
    CONSTRAINT fk_tm_subdistricts_district FOREIGN KEY (district_id) REFERENCES tm_districts (id)
);
CREATE INDEX IF NOT EXISTS idx_tm_subdistricts_district ON tm_subdistricts (district_id);
CREATE INDEX IF NOT EXISTS idx_tm_subdistricts_name ON tm_subdistricts (name);
CREATE INDEX IF NOT EXISTS idx_tm_subdistricts_deleted ON tm_subdistricts (deleted_at);