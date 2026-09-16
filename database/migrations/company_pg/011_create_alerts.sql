-- ============================================================================
-- Migration: COMPANY 011 — th_alerts + td_notifications (B3, PRD §5.9, §6.3)
-- ============================================================================
-- th_alerts: transaction header per alert. Life-cycle open → acknowledged →
-- resolved (acknowledge via API, resolve manual or automatic — e.g. OFFLINE
-- alert resolves when the vehicle reports again). TTA (time-to-acknowledge) is
-- recorded ONCE per alert at the first open → acknowledged transition.
-- Dedup: one OPEN alert per `dedup_key` (type-scoped identity) enforced by a
-- partial unique index; worker-alert inserts with ON CONFLICT DO NOTHING.
-- Like th_telemetry_logs, th_alerts rows are never soft-deleted per row (§6.3).

CREATE TABLE IF NOT EXISTS th_alerts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type VARCHAR(30) NOT NULL CHECK (type IN
        ('geofence_breach', 'overspeeding', 'battery_low', 'offline', 'sos',
         'route_deviation', 'fuel_drop', 'refuel')),
    severity VARCHAR(10) NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    lat DECIMAL(10, 8),
    lon DECIMAL(11, 8),
    speed DOUBLE PRECISION,
    metadata JSONB,
    status VARCHAR(15) NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'acknowledged', 'resolved')),
    acknowledged_by BIGINT,
    acknowledged_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT,
    sos_time_to_acknowledge_seconds INT,
    escalation_count INT NOT NULL DEFAULT 0,
    dedup_key VARCHAR(150) NOT NULL,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- One OPEN alert per dedup identity (e.g. "sos:ACME:123", "geofence:ACME:123:7:entry").
CREATE UNIQUE INDEX IF NOT EXISTS uq_th_alerts_open_dedup
    ON th_alerts (dedup_key) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_th_alerts_vehicle_time ON th_alerts (vehicle_id, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_th_alerts_type ON th_alerts (type);
CREATE INDEX IF NOT EXISTS idx_th_alerts_severity ON th_alerts (severity);
CREATE INDEX IF NOT EXISTS idx_th_alerts_status ON th_alerts (status);
CREATE INDEX IF NOT EXISTS idx_th_alerts_detected ON th_alerts (detected_at DESC);

-- td_notifications: delivery audit per alert × user × channel
-- (pending → sent|delivered → failed|skipped + reason, PRD §5.9.8).
CREATE TABLE IF NOT EXISTS td_notifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    alert_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    channel VARCHAR(10) NOT NULL CHECK (channel IN ('websocket', 'email', 'sms', 'push')),
    status VARCHAR(10) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'delivered', 'failed', 'skipped')),
    provider_response JSONB,
    error_reason TEXT,
    attempts INT NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ,

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_td_notifications_alert FOREIGN KEY (alert_id)
        REFERENCES th_alerts (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_td_notifications_alert ON td_notifications (alert_id);
CREATE INDEX IF NOT EXISTS idx_td_notifications_user ON td_notifications (user_id, channel);
CREATE INDEX IF NOT EXISTS idx_td_notifications_status ON td_notifications (status);