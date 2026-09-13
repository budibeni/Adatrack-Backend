-- ============================================================================
-- Migration: COMPANY 015 — media_events (PostgreSQL)
-- ============================================================================
-- Mitra PostgreSQL dari migration 015 MySQL (B5b, PRD v1.3.0 Module 8).
-- ENUM direpresentasikan sebagai VARCHAR + CHECK (pola company_pg untuk
-- alerts/notification dst); meta → jsonb.
-- ============================================================================

CREATE TABLE IF NOT EXISTS media_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    media_type VARCHAR(16) NOT NULL CHECK (media_type IN ('photo','video_clip')),
    trigger_type VARCHAR(16) NOT NULL CHECK (trigger_type IN ('sos','alarm','geofence','overspeed','manual','scheduled','power')),
    object_key VARCHAR(255) NOT NULL,
    bucket VARCHAR(63) NOT NULL,
    size_bytes BIGINT DEFAULT 0,
    duration_seconds INT,
    mime_type VARCHAR(64) NOT NULL,
    sha256 VARCHAR(64),
    status VARCHAR(12) NOT NULL DEFAULT 'uploaded' CHECK (status IN ('uploaded','available','expired','failed')),
    taken_at TIMESTAMP NOT NULL,
    meta JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP,
    CONSTRAINT fk_media_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id)
);

CREATE INDEX IF NOT EXISTS idx_media_vehicle_taken ON media_events (vehicle_id, taken_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_trigger ON media_events (trigger_type, taken_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_status ON media_events (status);

-- ============================================================================
-- End of COMPANY 015 (postgres)
-- ============================================================================