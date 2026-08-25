-- ============================================================================
-- Migration: COMPANY 010 — notifications (Sent Notification History, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS notifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    alert_id BIGINT,
    company_code VARCHAR(20) NOT NULL,
    channel TEXT NOT NULL CHECK (channel IN ('websocket','email','sms','push')),
    alert_type VARCHAR(50),
    subject VARCHAR(255),
    body TEXT,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','sent','delivered','failed','skipped')),
    provider_response JSONB,
    error_message TEXT,
    retry_count INT DEFAULT 0,
    sent_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ntf_user ON notifications (user_id);
CREATE INDEX IF NOT EXISTS idx_ntf_alert ON notifications (alert_id);
CREATE INDEX IF NOT EXISTS idx_ntf_company ON notifications (company_code);
CREATE INDEX IF NOT EXISTS idx_ntf_status ON notifications (status);
CREATE INDEX IF NOT EXISTS idx_ntf_created ON notifications (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ntf_channel_status ON notifications (channel, status);

-- ============================================================================
-- End of COMPANY 010 (postgres)
-- ============================================================================