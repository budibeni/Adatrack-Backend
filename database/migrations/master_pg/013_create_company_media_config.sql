-- ============================================================================
-- Migration: MASTER 013 — company_media_config (PostgreSQL)
-- ============================================================================
-- Mitra PostgreSQL dari migration 013 MySQL (B5b, PRD v1.3.0 Module 8).
-- hmac_secret per-company untuk ingest endpoint service-media (FR-8.1).
-- ============================================================================

CREATE TABLE IF NOT EXISTS company_media_config (
    company_code   VARCHAR(20) PRIMARY KEY,
    bucket         VARCHAR(63)  NOT NULL,
    retention_days INT          NOT NULL DEFAULT 30,
    max_file_mb    INT          NOT NULL DEFAULT 100,
    hmac_secret    VARCHAR(128) NOT NULL,
    created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ============================================================================
-- End of MASTER 013 (postgres)
-- ============================================================================