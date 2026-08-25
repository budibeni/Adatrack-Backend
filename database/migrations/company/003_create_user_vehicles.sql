-- ============================================================================
-- Migration: COMPANY 003 — user_vehicles (RBAC mapping)
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_vehicles (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                        -- references user_company_access.user_id
    vehicle_id BIGINT NOT NULL,
    assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY unique_user_vehicle (user_id, vehicle_id),
    FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    INDEX idx_user (user_id),
    INDEX idx_vehicle (vehicle_id)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 003
-- ============================================================================