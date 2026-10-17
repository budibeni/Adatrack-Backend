-- ==============================================================================
-- Migration 040: Ensure Unique Constraints on Menu Access in Tenant Schemas
-- ==============================================================================

CREATE UNIQUE INDEX IF NOT EXISTS uq_role_menu ON tm_role_menu_access (role_code, menu_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_menu ON tm_user_menu_access (user_id, menu_id);
