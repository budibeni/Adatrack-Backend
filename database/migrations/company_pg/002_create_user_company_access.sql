-- ============================================================================
-- Migration: COMPANY 002 — tm_user_company_access (tenant RBAC registry)
-- ============================================================================
-- user_id references master.tm_users.id LOGICALLY (no cross-schema FK — the
-- company schema name is dynamic). role_override replaces the global role inside
-- this tenant; permissions carries per-tenant extras (JSONB).

CREATE TABLE IF NOT EXISTS tm_user_company_access (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    role_override VARCHAR(20) CHECK (role_override IN ('Admin', 'Manager', 'Operator', 'Driver')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    permissions JSONB,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_user_company_access_user UNIQUE (user_id)
);
CREATE INDEX IF NOT EXISTS idx_tm_user_company_access_role ON tm_user_company_access (role_override);
CREATE INDEX IF NOT EXISTS idx_tm_user_company_access_active ON tm_user_company_access (is_active);
CREATE INDEX IF NOT EXISTS idx_tm_user_company_access_deleted ON tm_user_company_access (deleted_at);