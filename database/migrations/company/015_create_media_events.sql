-- ============================================================================
-- Migration: COMPANY 015 — media_events (B5b, PRD v1.3.0 Module 8)
-- ============================================================================
-- Katalog media dashcam per company (FR-8.3). Siklus status:
--   uploaded → available → expired | failed
-- Soft-delete via deleted_at (FR-8.4). `meta` menyimpan metadata vendor tambahan.
-- FK vehicle_id → vehicles(id) (tabel ini TIDAK ter-partisi → FK diizinkan).
-- Key layout object: {company}/{vehicle}/{yyyyMM}/{uuid} (FR-8.2).
-- ============================================================================

CREATE TABLE IF NOT EXISTS media_events (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    media_type ENUM('photo','video_clip') NOT NULL,
    trigger_type ENUM('sos','alarm','geofence','overspeed','manual','scheduled','power') NOT NULL,
    object_key VARCHAR(255) NOT NULL,
    bucket VARCHAR(63) NOT NULL,
    size_bytes BIGINT DEFAULT 0,
    duration_seconds INT NULL,
    mime_type VARCHAR(64) NOT NULL,
    sha256 CHAR(64) NULL,
    status ENUM('uploaded','available','expired','failed') DEFAULT 'uploaded',
    taken_at DATETIME NOT NULL,
    meta JSON NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    CONSTRAINT fk_media_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    INDEX idx_media_vehicle_taken (vehicle_id, taken_at DESC),
    INDEX idx_media_trigger (trigger_type, taken_at DESC),
    INDEX idx_media_status (status)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 015
-- ============================================================================