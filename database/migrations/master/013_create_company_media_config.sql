-- ============================================================================
-- Migration: MASTER 013 — company_media_config (B5b, PRD v1.3.0 Module 8)
-- ============================================================================
-- Konfigurasi object-storage & ingest per-company (MASTER DB). hmac_secret
-- dipakai untuk validasi header X-Signature pada endpoint ingest service-media
-- (FR-8.1). Per-company bucket & limit; fallback global env pada service-media
-- bila baris ini belum ada.
-- ============================================================================

CREATE TABLE IF NOT EXISTS company_media_config (
    company_code   VARCHAR(20) PRIMARY KEY,
    bucket         VARCHAR(63)  NOT NULL,
    retention_days INT          NOT NULL DEFAULT 30,
    max_file_mb    INT          NOT NULL DEFAULT 100,
    hmac_secret    VARCHAR(128) NOT NULL,
    created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 013
-- ============================================================================