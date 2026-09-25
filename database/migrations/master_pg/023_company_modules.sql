-- ============================================================================
-- Migration: MASTER 023 — tm_company_modules (module licensing, B12)
-- ============================================================================
-- Industry-specific modules of docs/FRONTEND.md §1.7 (Rental, Transport,
-- Logistics, Sales, Field Service, Patrol, Project Site) are enabled PER TENANT
-- ("bertahap, per flag lisensi tenant" — B12 task 11). Core modules
-- (main/master-data/access/asset/safety/analysis/admin + Personal
-- tracking/statistics/settings) are enabled by default and only need a row to be
-- explicitly disabled.
--
-- Runtime defaults live in api-vehicle (`moduleDefaultEnabled`): a tenant
-- provisioned AFTER this migration simply has no row and falls back to those
-- defaults, so no cross-schema seeding is required at provisioning time. The seed
-- below only materialises the default for companies that already exist.
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_company_modules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    module_code VARCHAR(64) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    licensed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    notes TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_company_modules UNIQUE (company_code, module_code)
);
CREATE INDEX IF NOT EXISTS idx_tm_company_modules_company ON tm_company_modules (company_code, enabled);

-- Core modules: enabled for every existing company (idempotent).
INSERT INTO tm_company_modules (company_code, module_code, enabled, licensed_at)
SELECT c.code, m.code, TRUE, CURRENT_TIMESTAMP
FROM tm_companies c
CROSS JOIN (VALUES
    ('main'), ('master-data'), ('access'), ('asset'), ('safety'),
    ('analysis'), ('industry'), ('admin'),
    ('tracking'), ('statistics'), ('settings')
) AS m(code)
ON CONFLICT (company_code, module_code) DO NOTHING;

-- Industry sub-modules: opt-in (licence required).
INSERT INTO tm_company_modules (company_code, module_code, enabled)
SELECT c.code, m.code, FALSE
FROM tm_companies c
CROSS JOIN (VALUES
    ('rental'), ('transport'), ('logistics'), ('sales'),
    ('field-service'), ('patrol'), ('project-site')
) AS m(code)
ON CONFLICT (company_code, module_code) DO NOTHING;
