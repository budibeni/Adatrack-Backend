-- ============================================================================
-- Migration: COMPANY 011 — routes (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS routes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    waypoints JSONB NOT NULL,
    estimated_duration_sec INT,
    created_by BIGINT NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_rte_created FOREIGN KEY (created_by) REFERENCES user_company_access(user_id)
);
CREATE INDEX IF NOT EXISTS idx_rte_active ON routes (is_active);
CREATE INDEX IF NOT EXISTS idx_rte_created ON routes (created_by);

-- ============================================================================
-- End of COMPANY 011 (postgres)
-- ============================================================================