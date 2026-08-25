-- ============================================================================
-- Migration: COMPANY 007 — speed_configs (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS speed_configs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT,
    speed_limit_kmh DOUBLE PRECISION NOT NULL,
    grace_margin_kmh DOUBLE PRECISION DEFAULT 5.0,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_spd_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_spd_vehicle ON speed_configs (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_spd_active ON speed_configs (is_active);

-- ============================================================================
-- End of COMPANY 007 (postgres)
-- ============================================================================