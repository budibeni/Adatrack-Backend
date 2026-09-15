-- ============================================================================
-- Migration: MASTER 018 — tm_company_media_config (PRD §6.1, consumed by B5b)
-- ============================================================================
-- Per-company dashcam media configuration (bucket, retention, limits, HMAC
-- secret). The table is created in B0 so the master schema is stable for the
-- service-media phase (B5b); no media code ships before that phase.

CREATE TABLE IF NOT EXISTS tm_company_media_config (
    company_code VARCHAR(20) PRIMARY KEY,
    bucket VARCHAR(128) NOT NULL,
    retention_days INT NOT NULL DEFAULT 30,
    max_file_mb INT NOT NULL DEFAULT 100,
    hmac_secret VARCHAR(255),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tm_company_media_config_company FOREIGN KEY (company_code) REFERENCES tm_companies (code)
);
CREATE INDEX IF NOT EXISTS idx_tm_company_media_config_deleted ON tm_company_media_config (deleted_at);