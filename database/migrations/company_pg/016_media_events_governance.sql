-- 016_media_events_governance.sql (B5b — Dashcam Event Media Scope A, PRD Module 8)
-- Additive pelengkap `th_media_events` (migrasi 015) untuk tata kelola§6.0.1/§9.4:
--   * deleted_by        → aktor soft delete (FR-8.9, pola `deleted_at`/`delete_reason`).
--   * upload_source     → jalur ingest yang dipakai ('multipart' | 'json' = presigned PUT),
--                         dipakai metrik media_uploads_total{company_code,media_type} + audit.
--   * object_etag       → ETag object storage (idempotensi complete + verifikasi byte).
--   * retention_days    → snapshot kebijakan retensi per-company saat upload (FR-8.7),
--                         sehingga perubahan config tidak mengubah baris lama.
--   * notified_at       → waktu publish `media.event.<company>` (FR-8.5) — sekali saja.
-- Additive-only (§1 global rules): kolom baru dengan DEFAULT/nullable, tidak ada
-- perubahan/penghapusan kolom lama → aman dijalankan ulang (idempoten).

ALTER TABLE th_media_events
    ADD COLUMN IF NOT EXISTS deleted_by     BIGINT,
    ADD COLUMN IF NOT EXISTS upload_source  VARCHAR(20) NOT NULL DEFAULT 'multipart',
    ADD COLUMN IF NOT EXISTS object_etag    VARCHAR(128),
    ADD COLUMN IF NOT EXISTS retention_days INT,
    ADD COLUMN IF NOT EXISTS notified_at    TIMESTAMPTZ;

-- Allowlist jalur ingest (PRD §8.5 rule 2: whitelist, bukan blacklist). Guard
-- idempoten: constraint ditambahkan hanya bila belum ada.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'th_media_events_upload_source_check'
    ) THEN
        ALTER TABLE th_media_events
            ADD CONSTRAINT th_media_events_upload_source_check
            CHECK (upload_source IN ('multipart', 'json'));
    END IF;
END $$;

-- Jalur hot retensi: baris `complete` yang belum lewat expiry, dan baris
-- pending yang menggantung (upload JSON yang tidak pernah di-complete).
CREATE INDEX IF NOT EXISTS idx_media_events_pending_age
    ON th_media_events (status, created_at)
    WHERE status = 'pending';
