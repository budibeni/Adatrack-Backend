-- ============================================================================
-- Migration: MASTER 014 — tm_modules (application module registry, v1.7.0)
-- ============================================================================
-- Global catalogue seeded from docs/FRONTEND.md §1–§3 (B12 consumes it, but the
-- registry is created in B0 so the menu/navigation contract exists from day one).
--
--   app = business → ADATRACK Business (FRONTEND.md §1)
--   app = personal → ADATRACK Personal (FRONTEND.md §2)

CREATE TABLE IF NOT EXISTS tm_modules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(100) NOT NULL,
    app VARCHAR(10) NOT NULL DEFAULT 'business' CHECK (app IN ('business', 'personal')),
    description VARCHAR(255),
    sort_order INT NOT NULL DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_modules_code UNIQUE (code)
);
CREATE INDEX IF NOT EXISTS idx_tm_modules_app ON tm_modules (app, sort_order);
CREATE INDEX IF NOT EXISTS idx_tm_modules_enabled ON tm_modules (enabled);