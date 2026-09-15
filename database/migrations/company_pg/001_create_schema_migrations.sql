-- ============================================================================
-- Migration: COMPANY 001 — tm_schema_migrations (per-tenant ledger, PRD §14.5)
-- ============================================================================
-- Every tenant schema keeps its own ledger so auto-provisioning can verify that
-- all company migrations were applied (ledger applied == number of files).

CREATE TABLE IF NOT EXISTS tm_schema_migrations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope VARCHAR(64) NOT NULL,
    version VARCHAR(255) NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    success BOOLEAN NOT NULL DEFAULT TRUE,
    applied_by VARCHAR(64) NOT NULL DEFAULT CURRENT_USER,
    CONSTRAINT uq_schema_migrations UNIQUE (scope, version)
);
CREATE INDEX IF NOT EXISTS idx_tm_schema_migrations_scope ON tm_schema_migrations (scope, applied_at DESC);