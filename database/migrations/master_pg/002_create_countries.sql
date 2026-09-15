-- ============================================================================
-- Migration: MASTER 002 — tm_countries (ISO 3166-1 reference)
-- ============================================================================
-- Reference data. Soft-delete columns are present on every master table from
-- the first migration (§6.0.1) even when reference rows are never deleted.

CREATE TABLE IF NOT EXISTS tm_countries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    iso_code VARCHAR(2) NOT NULL,
    iso_code_3 VARCHAR(3),
    name VARCHAR(100) NOT NULL,
    phone_code VARCHAR(10),
    currency_code VARCHAR(3),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_countries_iso UNIQUE (iso_code)
);
CREATE INDEX IF NOT EXISTS idx_tm_countries_name ON tm_countries (name);
CREATE INDEX IF NOT EXISTS idx_tm_countries_deleted ON tm_countries (deleted_at);