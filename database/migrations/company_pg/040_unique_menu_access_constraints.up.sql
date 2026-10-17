-- ==============================================================================
-- Migration 040: Ensure Unique Constraints on Menu Access in Tenant Schemas
-- ==============================================================================

-- Deduplicate any existing duplicate role menu access
DELETE FROM tm_role_menu_access a USING tm_role_menu_access b
WHERE a.id < b.id AND a.role_code = b.role_code AND a.menu_id = b.menu_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_role_menu ON tm_role_menu_access (role_code, menu_id);

-- Deduplicate any existing duplicate user menu access
DELETE FROM tm_user_menu_access a USING tm_user_menu_access b
WHERE a.id < b.id AND a.user_id = b.user_id AND a.menu_id = b.menu_id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_user_menu ON tm_user_menu_access (user_id, menu_id);
