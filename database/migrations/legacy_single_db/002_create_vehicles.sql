-- ============================================================================
-- Migration: 002_create_vehicles_table
-- ============================================================================
-- Vehicles table storing vehicle/GPS device information
-- NOTE: urutan migrasi = users (001) -> vehicles (002) -> user_vehicles (003)
--       (user_vehicles punya FK ke users & vehicles, jadi harus setelah keduanya)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicles (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    imei VARCHAR(15) NOT NULL UNIQUE,
    device_model VARCHAR(100) NULL,
    firmware_version VARCHAR(50) NULL,
    status ENUM('ACTIVE', 'INACTIVE', 'OFFLINE', 'MAINTENANCE') NOT NULL DEFAULT 'ACTIVE',
    last_seen_at TIMESTAMP NULL,
    current_latitude DECIMAL(10, 8) NULL,
    current_longitude DECIMAL(11, 8) NULL,
    current_speed DECIMAL(5, 2) NULL,
    user_id BIGINT UNSIGNED NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE INDEX uq_vehicles_imei (imei),
    INDEX idx_vehicles_user_id (user_id),
    INDEX idx_vehicles_status (status),
    INDEX idx_vehicles_last_seen (last_seen_at),
    INDEX idx_vehicles_deleted_at (deleted_at),
    -- B0: MySQL tdk mendukung SPATIAL INDEX pd kolom DECIMAL (non-geometri).
    -- Query geofence memakai composite index (lat, lon) ini. Bila butuh spatial,
    -- tambahkan kolom POINT + SPATIAL INDEX (di luar cakupan B0, per PRD §6).
    INDEX idx_vehicles_current_location (current_latitude, current_longitude)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 002
-- ============================================================================