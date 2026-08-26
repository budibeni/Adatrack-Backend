-- ============================================================================
-- Migration: COMPANY 014 — fuel_configs (PostgreSQL)
-- ============================================================================
-- Mitra PostgreSQL dari migration 014 MySQL (B5a, PRD v1.3.0 Module 7).
-- vehicle_id NULL = default global company; baris per-vehicle MENANG.
-- ============================================================================

CREATE TABLE IF NOT EXISTS fuel_configs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT DEFAULT NULL,
    drop_threshold DOUBLE PRECISION NOT NULL DEFAULT 10.0,
    refuel_threshold DOUBLE PRECISION NOT NULL DEFAULT 10.0,
    window_seconds INT NOT NULL DEFAULT 300,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_fc_vehicle UNIQUE (vehicle_id)
);

CREATE INDEX IF NOT EXISTS idx_fc_vehicle_enabled ON fuel_configs (vehicle_id, enabled);

-- ============================================================================
-- End of COMPANY 014 (postgres)
-- ===========================================================================