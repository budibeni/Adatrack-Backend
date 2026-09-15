-- ============================================================================
-- Migration: MASTER 003 — tm_companies (tenant registry, B2B/B2C)
-- ============================================================================
-- business_type distinguishes the two business models (PRD §4.2, §6.1):
--   b2b → multi-tenant company (default; auth via master.tm_users)
--   b2c → single-tenant consumer (auth via master.tm_users_b2c)

CREATE TABLE IF NOT EXISTS tm_companies (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,

    legal_name VARCHAR(255),
    company_email VARCHAR(255),
    website VARCHAR(255),
    tax_id VARCHAR(50),
    postal_code VARCHAR(10),

    country_code VARCHAR(2) NOT NULL,
    address TEXT,
    phone VARCHAR(20),
    timezone VARCHAR(50) NOT NULL DEFAULT 'Asia/Jakarta',
    business_type VARCHAR(3) NOT NULL DEFAULT 'b2b'
        CHECK (business_type IN ('b2b', 'b2c')),
    settings JSONB,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    activated_at TIMESTAMPTZ,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_companies_code UNIQUE (code),
    CONSTRAINT fk_tm_companies_country FOREIGN KEY (country_code) REFERENCES tm_countries (iso_code)
);
CREATE INDEX IF NOT EXISTS idx_tm_companies_code ON tm_companies (code);
CREATE INDEX IF NOT EXISTS idx_tm_companies_country ON tm_companies (country_code);
CREATE INDEX IF NOT EXISTS idx_tm_companies_active ON tm_companies (is_active);
CREATE INDEX IF NOT EXISTS idx_tm_companies_business_type ON tm_companies (business_type);
CREATE INDEX IF NOT EXISTS idx_tm_companies_tax ON tm_companies (tax_id);
CREATE INDEX IF NOT EXISTS idx_tm_companies_deleted ON tm_companies (deleted_at);