-- ============================================================================
-- Migration: COMPANY 005 — geofences
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofences (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    area_type ENUM('circle', 'polygon') NOT NULL,
    coordinates JSON NOT NULL,                      -- GeoJSON
    radius_meters INT,                               -- hanya untuk circle
    boundary_points JSON,                            -- hanya untuk polygon
    created_by BIGINT NOT NULL,                      -- references user_company_access.user_id
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by) REFERENCES user_company_access(user_id),
    INDEX idx_created_by (created_by),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 005
-- ============================================================================