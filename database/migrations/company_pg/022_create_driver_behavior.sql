-- ============================================================================
-- Migration: COMPANY 022 — B8 driver behaviour (PRD §21.2 row 4, FR-2.7)
-- ============================================================================
--   * `td_driver_events`  — one row per device-reported driver event (harsh
--     acceleration/braking/cornering) and per completed overspeed episode, with
--     the duration for speeding episodes.
--   * `th_driver_scores`  — daily score per vehicle, recomputed by worker-alert
--     from the events of that day.
--
-- The event TYPE is closed (CHECK), so a decoder can never invent a new event
-- class: only the pulses the device itself reports (GT06 alarm 0x29/0x30,
-- Teltonika IO 253/254/240) plus the derived `speeding` episode land here.
--
-- th_alerts' type CHECK is extended with the two new alert types of this phase
-- (driver_event, maintenance_due). PostgreSQL names the inline column CHECK
-- `th_alerts_type_check`, and dropping it IF EXISTS keeps the migration
-- idempotent for schemas created before this file existed.
-- ============================================================================

ALTER TABLE th_alerts DROP CONSTRAINT IF EXISTS th_alerts_type_check;
ALTER TABLE th_alerts ADD CONSTRAINT th_alerts_type_check CHECK (type IN
    ('geofence_breach', 'overspeeding', 'battery_low', 'offline', 'sos',
     'route_deviation', 'fuel_drop', 'refuel', 'driver_event', 'maintenance_due'));

CREATE TABLE IF NOT EXISTS td_driver_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    driver_id BIGINT,

    event_type VARCHAR(24) NOT NULL CHECK (event_type IN
        ('harsh_acceleration', 'harsh_braking', 'harsh_cornering', 'speeding')),
    severity VARCHAR(10) NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),

    speed_kmh NUMERIC(7, 2),
    speed_limit_kmh NUMERIC(7, 2),
    duration_seconds INT NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    lat DECIMAL(10, 8),
    lon DECIMAL(11, 8),

    -- Provenance: `device_alarm` (GT06 alarm reason), `io_event` (Teltonika IO
    -- element) or `derived` (overspeed episode measured server-side).
    source VARCHAR(16) NOT NULL DEFAULT 'device_alarm' CHECK (source IN
        ('device_alarm', 'io_event', 'derived')),
    raw_code INT,

    "timestamp" TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_td_driver_events_vehicle_time
    ON td_driver_events (vehicle_id, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_td_driver_events_company_type_time
    ON td_driver_events (company_code, event_type, "timestamp" DESC);
-- Episode rows are emitted once: this index makes the "one speeding row per
-- closed episode" property checkable and keeps the day-aggregate query cheap.
CREATE INDEX IF NOT EXISTS idx_td_driver_events_driver_time
    ON td_driver_events (driver_id, "timestamp" DESC);

CREATE TABLE IF NOT EXISTS th_driver_scores (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,
    driver_id BIGINT,

    period_start DATE NOT NULL,
    period_end DATE NOT NULL,

    harsh_acceleration_count INT NOT NULL DEFAULT 0 CHECK (harsh_acceleration_count >= 0),
    harsh_braking_count INT NOT NULL DEFAULT 0 CHECK (harsh_braking_count >= 0),
    harsh_cornering_count INT NOT NULL DEFAULT 0 CHECK (harsh_cornering_count >= 0),
    speeding_count INT NOT NULL DEFAULT 0 CHECK (speeding_count >= 0),
    speeding_seconds INT NOT NULL DEFAULT 0 CHECK (speeding_seconds >= 0),

    score NUMERIC(5, 2) NOT NULL DEFAULT 100 CHECK (score >= 0 AND score <= 100),
    grade VARCHAR(2) NOT NULL DEFAULT 'A' CHECK (grade IN ('A', 'B', 'C', 'D', 'E')),

    computed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT th_driver_scores_window_check CHECK (period_end >= period_start)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_th_driver_scores_vehicle_period
    ON th_driver_scores (vehicle_id, period_start);
CREATE INDEX IF NOT EXISTS idx_th_driver_scores_company_period
    ON th_driver_scores (company_code, period_start DESC);
