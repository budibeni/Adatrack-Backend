-- ============================================================================
-- Migration: COMPANY 009 — notification_preferences (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS notification_preferences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    alert_type VARCHAR(50) NOT NULL,
    channel TEXT NOT NULL CHECK (channel IN ('websocket','email','sms','push')),
    min_severity TEXT DEFAULT 'warning' CHECK (min_severity IN ('info','warning','critical')),
    delivery_config JSONB,
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_np_user FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    CONSTRAINT uq_np_user_alert_channel UNIQUE (user_id, alert_type, channel)
);
CREATE INDEX IF NOT EXISTS idx_np_user ON notification_preferences (user_id);
CREATE INDEX IF NOT EXISTS idx_np_enabled ON notification_preferences (is_enabled);
CREATE INDEX IF NOT EXISTS idx_np_severity ON notification_preferences (min_severity);

-- ============================================================================
-- End of COMPANY 009 (postgres)
-- ============================================================================