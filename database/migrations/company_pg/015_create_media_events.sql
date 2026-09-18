-- 015_create_media_events.sql (B5b — Dashcam Event Media Scope A, PRD Module 8)
-- Katalog event media per-tenant: foto + clip pendek saat sos/alarm/geofence/
-- overspeed/manual/scheduled/power. Lifecycle: pending → complete → expired →
-- deleted (soft delete + retention job). Objeknya sendiri tersimpan di object
-- storage (MinIO/S3); baris ini hanya metadata + audit.
CREATE TABLE IF NOT EXISTS th_media_events (
    id                 BIGSERIAL PRIMARY KEY,
    vehicle_id         BIGINT NOT NULL,
    imei               VARCHAR(30) NOT NULL,
    event_type         VARCHAR(30) NOT NULL CHECK (event_type IN
                       ('sos','alarm','geofence','overspeed','manual','scheduled','power')),
    object_key         TEXT NOT NULL UNIQUE,
    file_size          BIGINT NOT NULL DEFAULT 0 CHECK (file_size >= 0),
    mime_type          VARCHAR(100) NOT NULL DEFAULT 'application/octet-stream',
    content_sha256     CHAR(64) NOT NULL,
    status             VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN
                       ('pending','complete','expired','deleted')),
    hmac_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    captured_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by_user_id BIGINT,
    completed_at       TIMESTAMPTZ,
    expires_at         TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ,
    delete_reason      TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_events_vehicle_time
    ON th_media_events (vehicle_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_events_status_expires
    ON th_media_events (status, expires_at);
CREATE INDEX IF NOT EXISTS idx_media_events_event_type
    ON th_media_events (event_type, captured_at DESC);
