-- ============================================================================
-- Migration: COMPANY 009 — tm_speed_configs (B3, PRD §5.9.4)
-- ============================================================================
-- vehicle_id NULL = global default config; a vehicle-specific row wins over
-- the global one. severity is the baseline for a breach; a breach beyond
-- 1.5x the effective limit is escalated to `critical` by worker-alert.

CREATE TABLE IF NOT EXISTS tm_speed_configs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT,
    max_speed_kmh INT NOT NULL CHECK (max_speed_kmh > 0),
    grace_margin_percent INT NOT NULL DEFAULT 0 CHECK (grace_margin_percent BETWEEN 0 AND 100),
    alert_severity VARCHAR(10) NOT NULL DEFAULT 'medium'
        CHECK (alert_severity IN ('low', 'medium', 'high', 'critical')),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_tm_speed_configs_vehicle FOREIGN KEY (vehicle_id)
        REFERENCES tm_vehicles (id) ON DELETE CASCADE
);

-- Exactly ONE global row and at most ONE row per vehicle (soft-deleted rows are
-- excluded so a restore does not collide with a fresh config).
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_speed_configs_global
    ON tm_speed_configs (vehicle_id) WHERE vehicle_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_speed_configs_vehicle
    ON tm_speed_configs (vehicle_id) WHERE vehicle_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_speed_configs_enabled ON tm_speed_configs (enabled) WHERE deleted_at IS NULL;