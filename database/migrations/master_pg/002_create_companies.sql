-- ============================================================================
-- Migration: MASTER 002 — companies (Tenant Registry, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS companies (
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
    timezone VARCHAR(50) DEFAULT 'Asia/Jakarta',
    settings JSONB,
    is_active BOOLEAN DEFAULT TRUE,
    activated_at TIMESTAMP,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMP,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_companies_code UNIQUE (code),
    CONSTRAINT fk_companies_country FOREIGN KEY (country_code) REFERENCES countries(iso_code)
);
CREATE INDEX IF NOT EXISTS idx_cmp_code ON companies (code);
CREATE INDEX IF NOT EXISTS idx_cmp_country ON companies (country_code);
CREATE INDEX IF NOT EXISTS idx_cmp_active ON companies (is_active);
CREATE INDEX IF NOT EXISTS idx_cmp_tax ON companies (tax_id);
CREATE INDEX IF NOT EXISTS idx_cmp_deleted ON companies (deleted_at);

-- ============================================================================
-- End of MASTER 002 (postgres)
-- ============================================================================