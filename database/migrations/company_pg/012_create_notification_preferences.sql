-- ============================================================================
-- Migration: COMPANY 012 — tm_notification_preferences (B3, PRD §5.9.8)
-- ============================================================================
-- Per user × alert type × channel delivery preference. `alert_type = 'all'`
-- matches every alert type. `min_severity` is the LOWEST severity delivered
-- (low < medium < high < critical). A user with NO row for a channel falls back
-- to the documented default: websocket ON, email/SMS/push OFF (worker-alert).

CREATE TABLE IF NOT EXISTS tm_notification_preferences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    alert_type VARCHAR(30) NOT NULL DEFAULT 'all' CHECK (alert_type IN
        ('all', 'geofence_breach', 'overspeeding', 'battery_low', 'offline', 'sos',
         'route_deviation', 'fuel_drop', 'refuel')),
    channel VARCHAR(10) NOT NULL CHECK (channel IN ('websocket', 'email', 'sms', 'push')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    min_severity VARCHAR(10) NOT NULL DEFAULT 'low'
        CHECK (min_severity IN ('low', 'medium', 'high', 'critical')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_notification_preferences UNIQUE (user_id, alert_type, channel)
);
CREATE INDEX IF NOT EXISTS idx_tm_notification_preferences_user
    ON tm_notification_preferences (user_id) WHERE deleted_at IS NULL;