-- ============================================================================
-- Migration: COMPANY 006 — geofence_vehicles
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofence_vehicles (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    geofence_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (geofence_id) REFERENCES geofences(id) ON DELETE CASCADE,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    UNIQUE KEY unique_geofence_vehicle (geofence_id, vehicle_id),
    INDEX idx_geofence (geofence_id),
    INDEX idx_vehicle (vehicle_id)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 006
-- ============================================================================