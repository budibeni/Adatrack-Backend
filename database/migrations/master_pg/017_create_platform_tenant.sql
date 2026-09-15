-- ============================================================================
-- Migration: MASTER 017 — platform tenant (PRD §6.1)
-- ============================================================================
-- The `DEFAULT` company is the platform context: it owns the
-- `adatrack_gps_default` schema and is the fallback before any customer tenant
-- is registered. business_type = b2b (platform operates the B2B model).

-- The platform tenant references ID (Indonesia); the minimal country row is
-- ensured here so this migration never depends on the reference seed running
-- first (the full ISO catalogue arrives later via database/seed/reference and
-- upserts the same row — both statements are idempotent).
INSERT INTO tm_countries (iso_code, iso_code_3, name, phone_code, currency_code, is_active)
VALUES ('ID', 'IDN', 'Indonesia', '+62', 'IDR', TRUE)
ON CONFLICT (iso_code) DO UPDATE SET
    iso_code_3 = EXCLUDED.iso_code_3,
    name = EXCLUDED.name,
    phone_code = EXCLUDED.phone_code,
    currency_code = EXCLUDED.currency_code;

INSERT INTO tm_companies (code, name, legal_name, country_code, timezone, business_type, is_active, activated_at)
VALUES ('DEFAULT', 'ADATRACK Platform', 'ADATRACK Platform (internal)', 'ID', 'Asia/Jakarta', 'b2b', TRUE, CURRENT_TIMESTAMP)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    legal_name = EXCLUDED.legal_name,
    timezone = EXCLUDED.timezone,
    business_type = EXCLUDED.business_type,
    is_active = TRUE;