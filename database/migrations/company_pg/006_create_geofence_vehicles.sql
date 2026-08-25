-- ============================================================================
-- Migration: COMPANY 006 — geofence_vehicles (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofence_vehicles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    geofence_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_gv_geofence FOREIGN KEY (geofence_id) REFERENCES geofences(id) ON DELETE CASCADE,
    CONSTRAINT fk_gv_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    CONSTRAINT uq_geofence_vehicle UNIQUE (geofence_id, vehicle_id)
);
CREATE INDEX IF NOT EXISTS idx_gv_geofence ON geofence_vehicles (geofence_id);
CREATE INDEX IF NOT EXISTS idx_gv_vehicle ON geofence_vehicles (vehicle_id);

-- ============================================================================
-- End of COMPANY 006 (postgres)
-- ============================================================================