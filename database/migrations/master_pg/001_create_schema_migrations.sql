-- ============================================================================
-- Migration: MASTER 001 — schema migrations ledger (PRD §14.5)
-- ============================================================================
-- Versioned + checksum-guarded ledger. Auto-migration (scripts/migrate.sh and
-- the service boot migrator, internal/migrate.go) reads/writes this table.
-- A migration that has already been applied MUST NOT be edited (checksum drift
-- aborts the deploy); changes always arrive as a NEW versioned file.
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_schema_migrations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope VARCHAR(64) NOT NULL,                     -- 'master' | 'company:adatrack_gps_xxx'
    version VARCHAR(255) NOT NULL,                  -- file name, e.g. 003_create_companies.sql
    checksum VARCHAR(64) NOT NULL,                  -- sha256 of the file contents
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    duration_ms BIGINT NOT NULL DEFAULT 0,
    success BOOLEAN NOT NULL DEFAULT TRUE,
    applied_by VARCHAR(64) NOT NULL DEFAULT CURRENT_USER,
    CONSTRAINT uq_schema_migrations UNIQUE (scope, version)
);
CREATE INDEX IF NOT EXISTS idx_tm_schema_migrations_scope ON tm_schema_migrations (scope, applied_at DESC);