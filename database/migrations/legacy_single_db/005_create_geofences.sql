-- ============================================================================
-- Migration: 005_create_geofences_table
-- ============================================================================
-- Geofences table storing defined geographic zones
-- Supports circular (center + radius) and polygon zones
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofences (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(191) NOT NULL,
    description VARCHAR(255) NULL,
    geofence_type ENUM('CIRCLE', 'POLYGON') NOT NULL DEFAULT 'CIRCLE',
    -- For CIRCLE type: center point + radius in meters
    center_latitude DECIMAL(10, 8) NOT NULL,
    center_longitude DECIMAL(11, 8) NOT NULL,
    radius_meters DECIMAL(10, 2) NOT NULL,
    -- For POLYGON type: comma-separated lat,lon pairs stored as WKT
    polygon_wkt LONGTEXT NULL,
    -- Vehicle assignments
    vehicle_scope ENUM('ALL', 'ASSIGNED') NOT NULL DEFAULT 'ASSIGNED',
    -- Status & timing
    status ENUM('ACTIVE', 'INACTIVE') NOT NULL DEFAULT 'ACTIVE',
    trigger_on ENUM('ENTRY', 'EXIT', 'BOTH') NOT NULL DEFAULT 'BOTH',
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    INDEX idx_geofences_center (center_latitude, center_longitude),
    INDEX idx_geofences_type_status (geofence_type, status),
    INDEX idx_geofences_trigger (trigger_on),
    INDEX idx_geofences_deleted_at (deleted_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 005
-- ============================================================================