-- ============================================================================
-- Migration: MASTER 020 — repair platform SuperAdmin drift (B3 fix)
-- ============================================================================
-- WHY THIS EXISTS
-- The legacy dev seed in `init-pg/02_master_setup.sql` inserted the three DEV001
-- users with HARDCODED ids (1/3/4) and `ON CONFLICT (id) DO UPDATE SET
-- password_hash, global_role`. Because `tm_users` is shared with the platform
-- SuperAdmin (master 019), that upsert silently rewrote row id=1 — the platform
-- identity — into a DEV001 `Admin` holding the DEV001 dev password, and left
-- `admin@dev001.io` missing altogether. Result: BOTH documented dev credentials
-- were broken and the platform account was demoted (broken tenant provisioning).
--
-- 02_master_setup.sql is fixed (email conflict target, no hardcoded ids); this
-- migration repairs databases that already drifted:
--   * restores company context (`DEFAULT`) and role (`SuperAdmin`);
--   * restores the documented password ONLY IF the drift hash is present, so a
--     deliberately rotated production password is never clobbered.
--
-- Idempotent: re-running is a no-op once the row is healthy (acceptance:
-- "init-pg idempoten").
-- ============================================================================

SET search_path TO adatrack_gps_master;

UPDATE tm_users
SET company_id = (SELECT id FROM tm_companies WHERE code = 'DEFAULT'),
    company_code = 'DEFAULT',
    global_role = 'SuperAdmin',
    is_active = TRUE,
    email_verified = TRUE,
    deleted_at = NULL,
    deleted_by = NULL,
    delete_reason = NULL,
    password_hash = CASE
        -- DEV001 dev password `Admin@123` leaked onto the platform row by the
        -- legacy id-based upsert → restore the documented `Platform@123` hash.
        WHEN password_hash = '$2a$12$68Y3c8vQndkODvLUKj52RuC02x8yLdpJorBYysKXAkLvV9mgf3YTK'
            THEN '$2a$12$cciB/47ikqedX67B6ccsRu3XOJ2DcSmyWHhtYd.k.qBi.63roLw.W'
        ELSE password_hash
    END,
    updated_at = CURRENT_TIMESTAMP
WHERE email = 'platform@adatrackgps.local';

-- Any DEV001 row wearing the platform SuperAdmin role is drift by definition:
-- demote it back to the tenant Admin it was meant to be. (No-op on a clean DB.)
UPDATE tm_users
SET global_role = 'Admin',
    updated_at = CURRENT_TIMESTAMP
WHERE email <> 'platform@adatrackgps.local'
  AND company_code = 'DEFAULT'
  AND global_role = 'SuperAdmin';

-- Keep the identity sequence ahead of the rows actually present, so the next
-- auto-provisioned tenant admin (FR-5.5) can never collide with a seeded id.
SELECT setval(pg_get_serial_sequence('tm_users', 'id'),
              GREATEST((SELECT COALESCE(MAX(id), 1) FROM tm_users), 4));