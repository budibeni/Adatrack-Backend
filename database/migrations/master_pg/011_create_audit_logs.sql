-- ============================================================================
-- Migration: MASTER 011 — audit_logs (Phase B4 hardening, GAP #12, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT,
    company_code VARCHAR(20),
    event_type VARCHAR(50) NOT NULL,
    action VARCHAR(100) NOT NULL,
    entity VARCHAR(60),
    entity_id VARCHAR(64),
    ip_address VARCHAR(45),
    user_agent VARCHAR(255),
    details JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_user_time ON audit_logs (user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_company_time ON audit_logs (company_code, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_event_time ON audit_logs (event_type, created_at);

-- ============================================================================
-- End of MASTER 011 (postgres)
-- ============================================================================