-- ============================================================================
-- Migration: MASTER 013 — tm_audit_logs (audit trail, append-only, PRD §9.4)
-- ============================================================================
-- MANDATORY audit trail. APPEND-ONLY: no UPDATE / no DELETE (soft delete does
-- NOT apply to this table — retention follows the partition policy §11).
-- Immutability is enforced by a trigger so a bug can never mutate history.
-- Read access is restricted to SuperAdmin at the service layer (B2/B11).

CREATE TABLE IF NOT EXISTS tm_audit_logs (
    audit_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action VARCHAR(64) NOT NULL,
    outcome VARCHAR(16) NOT NULL DEFAULT 'success'
        CHECK (outcome IN ('success', 'failure', 'denied')),

    actor_user_id BIGINT,
    actor_email VARCHAR(255),
    actor_role VARCHAR(64),
    actor_ip VARCHAR(64),
    actor_user_agent VARCHAR(512),

    company_code VARCHAR(20),
    entity_type VARCHAR(64),
    entity_id VARCHAR(64),
    before_state JSONB,
    after_state JSONB,
    reason VARCHAR(255),
    request_id VARCHAR(64),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_audit_logs_action_time ON tm_audit_logs (action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tm_audit_logs_company_time ON tm_audit_logs (company_code, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tm_audit_logs_actor ON tm_audit_logs (actor_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tm_audit_logs_entity ON tm_audit_logs (entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_tm_audit_logs_request ON tm_audit_logs (request_id);

-- Append-only enforcement (belt & braces on top of the service rule).
CREATE OR REPLACE FUNCTION tm_audit_logs_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'tm_audit_logs is append-only (no UPDATE/DELETE allowed)';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_tm_audit_logs_no_update ON tm_audit_logs;
CREATE TRIGGER trg_tm_audit_logs_no_update
    BEFORE UPDATE OR DELETE ON tm_audit_logs
    FOR EACH ROW EXECUTE FUNCTION tm_audit_logs_immutable();