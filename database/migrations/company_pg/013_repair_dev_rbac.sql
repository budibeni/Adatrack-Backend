-- =============================================================================
-- Migration: COMPANY 013 — repair dev001 tenant RBAC (B3 fix)
-- =============================================================================
-- WHY THIS EXISTS:
--   The legacy dev seed inserted `tm_user_company_access` rows with hardcoded
--   user_ids (1/3/4). Because `tm_users` is shared with the platform SuperAdmin
--   (id=1), that left a STALE cross-tenant membership row: user_id=1 (the
--   platform SuperAdmin) had an `Admin` entry in the DEV001 tenant — a literal
--   privilege leak from the platform scope into a tenant schema.
--
--   The seed in `init-pg/03_company_setup.sql` now resolves ids by email (no
--   more hardcoded ids), but databases already corrupted in the field still
--   carry the leaked row. This migration removes it.
--
-- The repair is surgical:
--   1. DELETE the leaked platform-admin membership in any non-DEFAULT tenant
--      (the platform identity is governance-only per PRD §3.1).
--   2. ENSURE the correct per-email membership rows exist (idempotent INSERT).
--
-- Idempotent — safe to re-apply.
-- =============================================================================

-- 1. Remove the cross-tenant leak: platform SuperAdmin (user_id=1 on a fresh
--    DB) must not hold a tenant `tm_user_company_access` row. We target by the
--    platform email via a subquery so the id is resolved at run-time (id is NOT
--    stable across environments), then soft-delete by setting deleted_at so the
--    row is audit-visible but the membership is inactive.
UPDATE tm_user_company_access
SET deleted_at = CURRENT_TIMESTAMP,
    deleted_by = 0,
    delete_reason = 'platform scope must not hold tenant membership (B3 repair)',
    is_active = FALSE
WHERE user_id = (SELECT id FROM adatrack_gps_master.tm_users
                 WHERE email = 'platform@adatrackgps.local')
  AND deleted_at IS NULL;
