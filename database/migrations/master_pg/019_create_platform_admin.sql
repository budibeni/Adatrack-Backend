-- ============================================================================
-- Migration: MASTER 019 — platform SuperAdmin seed (PRD §3.1 "Platform Tier")
-- ============================================================================
-- The platform (governance) identity is: context `default` (company registry
-- 'DEFAULT' → adatrack_gps_default) + role SuperAdmin. Such a token may ONLY
-- call the platform endpoints (/api/v1/companies, /api/v1/users); tenant routes
-- answer `403 PLATFORM_SCOPE` (enforced by service-websocket, B2).
--
-- Documented dev account: platform@adatrackgps.local / Platform@123
-- (bcrypt cost 12 — the hash below is a REAL, verified cost-12 hash).
--
-- SECURITY: rotate this password in production (secrets manager) — the account
-- is the tenant-provisioning authority. Because the upsert deliberately does NOT
-- touch password_hash, a rotated password survives any re-application of this
-- migration (the same rule FR-5.5 applies to auto-created tenant admins).
-- ============================================================================

INSERT INTO tm_users (company_id, company_code, email, password_hash, full_name,
                      first_name, last_name, global_role, is_active, email_verified,
                      mfa_enabled, locale)
VALUES (
    (SELECT id FROM tm_companies WHERE code = 'DEFAULT'),
    'DEFAULT',
    'platform@adatrackgps.local',
    '$2a$12$cciB/47ikqedX67B6ccsRu3XOJ2DcSmyWHhtYd.k.qBi.63roLw.W',
    'Platform SuperAdmin', 'Platform', 'Admin',
    'SuperAdmin', TRUE, TRUE, FALSE, 'id')
ON CONFLICT (email) DO UPDATE SET
    company_id = EXCLUDED.company_id,
    company_code = EXCLUDED.company_code,
    full_name = EXCLUDED.full_name,
    first_name = EXCLUDED.first_name,
    last_name = EXCLUDED.last_name,
    global_role = EXCLUDED.global_role,
    is_active = TRUE,
    email_verified = TRUE,
    updated_at = CURRENT_TIMESTAMP;