-- ============================================================================
-- Migration: COMPANY 013 — tm_fuel_configs (B5a, PRD §5.9.9 / Module 7)
-- ============================================================================
-- vehicle_id NULL = global default config; a vehicle-specific row wins over
-- the global one (same precedence rule as tm_speed_configs, B3).
--
-- Thresholds are percentages of the tank capacity (or the configured range).
--   * drop_threshold_percent  — FUEL_DROP severity: drop beyond this in window.
--   * refuel_threshold_percent — REFUEL severity: rise beyond this.
--   * window_seconds — sliding window over which the delta is evaluated.
--   * require_acc — ACC-gate (2026-08-26 decision): when true, FUEL_DROP is
--     only evaluated while ACC is stale (parked / ignition off); default
--     false = always evaluate (anti-siphon while parked).
--   * acc_stale_seconds — staleness window for the ACC gate.

CREATE TABLE IF NOT EXISTS tm_fuel_configs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT,
    drop_threshold_percent INT NOT NULL CHECK (drop_threshold_percent BETWEEN 1 AND 100),
    refuel_threshold_percent INT NOT NULL CHECK (refuel_threshold_percent BETWEEN 1 AND 100),
    window_seconds INT NOT NULL DEFAULT 300 CHECK (window_seconds BETWEEN 1 AND 86400),
    alert_severity VARCHAR(10) NOT NULL DEFAULT 'high'
        CHECK (alert_severity IN ('low', 'medium', 'high', 'critical')),
    require_acc BOOLEAN NOT NULL DEFAULT FALSE,
    acc_stale_seconds INT NOT NULL DEFAULT 600,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_tm_fuel_configs_vehicle FOREIGN KEY (vehicle_id)
        REFERENCES tm_vehicles (id) ON DELETE CASCADE
);

-- Exactly ONE global row and at most ONE row per vehicle (soft-deleted rows excluded).
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_fuel_configs_global
    ON tm_fuel_configs (vehicle_id) WHERE vehicle_id IS NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_fuel_configs_vehicle
    ON tm_fuel_configs (vehicle_id) WHERE vehicle_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_fuel_configs_enabled
    ON tm_fuel_configs (enabled) WHERE deleted_at IS NULL;
