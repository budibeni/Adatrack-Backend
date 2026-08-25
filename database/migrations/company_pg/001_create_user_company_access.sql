-- ============================================================================
-- Migration: COMPANY 001 — user_company_access (PostgreSQL)
-- ============================================================================
-- user_id mereferensi master.users.id (NO cross-DB FK).
-- Berisi user khas company ini termasuk admin masing-masing.
-- Role/permissions di-override per company via role_override (PRD §6.2).
-- PostgreSQL flavour: identity, CHECK constraint menggantikan ENUM, JSONB.
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_company_access (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,                        -- references master.users.id (NO cross-DB FK)
    role_override VARCHAR(20),                      -- NULL = pakai global_role dari master.users
    is_active BOOLEAN DEFAULT TRUE,
    permissions JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_uca_user UNIQUE (user_id)
);
CREATE INDEX IF NOT EXISTS idx_uca_role_override ON user_company_access (role_override);
CREATE INDEX IF NOT EXISTS idx_uca_active ON user_company_access (is_active);

-- ============================================================================
-- End of COMPANY 001 (postgres)
-- ============================================================================