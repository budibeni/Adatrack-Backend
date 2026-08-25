-- ============================================================================
-- Migration: 008_create_geofence_vehicles_table
-- ============================================================================
-- Junction table mapping geofences to vehicles for geofence monitoring
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofence_vehicles (
    geofence_id BIGINT UNSIGNED NOT NULL,
    vehicle_id BIGINT UNSIGNED NOT NULL,
    assigned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (geofence_id, vehicle_id),
    INDEX idx_geofence_vehicles_vehicle_id (vehicle_id),
    CONSTRAINT fk_geo_vehicles_geofence FOREIGN KEY (geofence_id) REFERENCES geofences(id) ON DELETE CASCADE,
    CONSTRAINT fk_geo_vehicles_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 008
-- ============================================================================