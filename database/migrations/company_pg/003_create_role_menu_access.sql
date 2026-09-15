-- ============================================================================
-- Migration: COMPANY 003 — tm_role_menu_access (role → menu, PER TENANT)
-- ============================================================================
-- Lives in the COMPANY schema (PRD §6.2), NOT in master: two tenants may grant
-- different menus to the same role.
-- menu_id references master.tm_menus.id LOGICALLY (no hard FK — dynamic schema
-- name; integrity is maintained by the service layer + the idempotent seed in
-- migration 004). Frontend renders navigation from GET /api/v1/access/menu.

CREATE TABLE IF NOT EXISTS tm_role_menu_access (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    role VARCHAR(20) NOT NULL
        CHECK (role IN ('SuperAdmin', 'Admin', 'Manager', 'Operator', 'Driver')),
    menu_id BIGINT NOT NULL,
    can_view BOOLEAN NOT NULL DEFAULT TRUE,
    can_create BOOLEAN NOT NULL DEFAULT FALSE,
    can_edit BOOLEAN NOT NULL DEFAULT FALSE,
    can_delete BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_role_menu_access UNIQUE (role, menu_id)
);
CREATE INDEX IF NOT EXISTS idx_tm_role_menu_access_role ON tm_role_menu_access (role);
CREATE INDEX IF NOT EXISTS idx_tm_role_menu_access_menu ON tm_role_menu_access (menu_id);
CREATE INDEX IF NOT EXISTS idx_tm_role_menu_access_enabled ON tm_role_menu_access (enabled);