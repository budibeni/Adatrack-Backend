-- ============================================================================
-- Migration: 010_create_routes_table
-- ============================================================================
-- Route management to enforce driver discipline (B3).
-- Assign a route to a driver/vehicle, track status lifecycle
-- (NOT_STARTED -> IN_PROGRESS -> COMPLETED | DELAYED) and detect deviation
-- of the vehicle from the planned waypoints.
-- ============================================================================

CREATE TABLE IF NOT EXISTS routes (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(191) NOT NULL,
    -- driver_id refers to a users row (role DRIVER/adatrack_MANAGER).
    driver_id BIGINT UNSIGNED NULL,
    vehicle_id BIGINT UNSIGNED NULL,
    -- JSON array of waypoints: [{"lat":..,"lon":..}, ...]
    waypoints JSON NOT NULL,
    status ENUM('NOT_STARTED','IN_PROGRESS','COMPLETED','DELAYED') NOT NULL DEFAULT 'NOT_STARTED',
    -- Amount of measured deviation events detected by worker-alert.
    deviation_count INT UNSIGNED NOT NULL DEFAULT 0,
    -- Deviation threshold in meters: beyond this distance from the nearest
    -- waypoint the vehicle is considered off-route.
    deviation_threshold_m DECIMAL(10, 2) NOT NULL DEFAULT 100.00,
    estimated_duration_sec INT UNSIGNED NULL,
    actual_duration_sec INT UNSIGNED NULL,
    start_time TIMESTAMP NULL,
    completed_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    INDEX idx_routes_driver_id (driver_id),
    INDEX idx_routes_vehicle_id (vehicle_id),
    INDEX idx_routes_status (status),
    CONSTRAINT fk_routes_driver_user FOREIGN KEY (driver_id) REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT fk_routes_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 010
-- ============================================================================
