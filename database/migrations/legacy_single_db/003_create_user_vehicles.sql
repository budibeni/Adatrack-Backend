-- ============================================================================
-- Migration: 003_create_user_vehicles_table
-- ============================================================================
-- Junction table mapping users to vehicles they can access
-- Implements RBAC at the database layer
-- NOTE: urutan = users (001) + vehicles (002) sudah harus ada (FK)
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_vehicles (
    user_id BIGINT UNSIGNED NOT NULL,
    vehicle_id BIGINT UNSIGNED NOT NULL,
    assigned_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    assigned_by BIGINT UNSIGNED NULL,
    PRIMARY KEY (user_id, vehicle_id),
    INDEX idx_user_vehicles_vehicle_id (vehicle_id),
    INDEX idx_user_vehicles_assigned_by (assigned_by),
    CONSTRAINT fk_user_vehicles_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_user_vehicles_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 003
-- ============================================================================