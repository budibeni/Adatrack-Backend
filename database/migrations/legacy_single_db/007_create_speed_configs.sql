-- ============================================================================
-- Migration: 007_create_speed_configs_table
-- ============================================================================
-- Speed configurations per vehicle or user group
-- Defines speed limits and grace margins for overspeed detection
-- ============================================================================

CREATE TABLE IF NOT EXISTS speed_configs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name VARCHAR(191) NOT NULL,
    description VARCHAR(255) NULL,
    speed_limit DECIMAL(5, 2) NOT NULL, -- Maximum allowed speed in km/h
    grace_margin DECIMAL(5, 2) NOT NULL DEFAULT 0.00, -- Grace period in km/h
    -- Scope: per vehicle or per user role
    scope ENUM('VEHICLE', 'USER_ROLE', 'ALL') NOT NULL DEFAULT 'VEHICLE',
    -- Associated vehicle/user
    vehicle_id BIGINT UNSIGNED NULL,
    user_role ENUM('ADMIN', 'adatrack_MANAGER', 'OPERATOR', 'DRIVER') NULL,
    -- Status
    status ENUM('ACTIVE', 'INACTIVE') NOT NULL DEFAULT 'ACTIVE',
    -- Metadata
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    UNIQUE INDEX uq_speed_configs_scope (scope, vehicle_id, user_role),
    INDEX idx_speed_configs_vehicle_id (vehicle_id),
    INDEX idx_speed_configs_user_role (user_role),
    INDEX idx_speed_configs_status (status)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 007
-- ============================================================================