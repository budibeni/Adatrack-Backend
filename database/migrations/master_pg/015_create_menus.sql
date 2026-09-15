-- ============================================================================
-- Migration: MASTER 015 — tm_menus (menu registry per module, v1.7.0)
-- ============================================================================
-- Every menu item carries its frontend route path so the dashboard can render
-- navigation dynamically from GET /api/v1/access/menu (B12). Per-role access
-- lives per tenant in tm_role_menu_access (company schema, migration 003).

CREATE TABLE IF NOT EXISTS tm_menus (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    module_id BIGINT NOT NULL,
    code VARCHAR(128) NOT NULL,
    name VARCHAR(100) NOT NULL,
    path VARCHAR(255),
    parent_id BIGINT,
    icon VARCHAR(64),
    sort_order INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_menus_code UNIQUE (code),
    CONSTRAINT fk_tm_menus_module FOREIGN KEY (module_id) REFERENCES tm_modules (id),
    CONSTRAINT fk_tm_menus_parent FOREIGN KEY (parent_id) REFERENCES tm_menus (id)
);
CREATE INDEX IF NOT EXISTS idx_tm_menus_module ON tm_menus (module_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_tm_menus_parent ON tm_menus (parent_id);
CREATE INDEX IF NOT EXISTS idx_tm_menus_path ON tm_menus (path);
CREATE INDEX IF NOT EXISTS idx_tm_menus_enabled ON tm_menus (enabled);