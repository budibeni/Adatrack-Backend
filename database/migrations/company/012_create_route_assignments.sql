-- ============================================================================
-- Migration: COMPANY 012 — route_assignments
-- ============================================================================
-- Assign route ke driver (user_company_access) + vehicle, lacak status
-- & deviation (B3).
-- ============================================================================

CREATE TABLE IF NOT EXISTS route_assignments (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    route_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    driver_user_id BIGINT NOT NULL,                    -- references user_company_access.user_id
    status ENUM('not_started', 'in_progress', 'completed', 'delayed') DEFAULT 'not_started',
    started_at TIMESTAMP NULL,
    completed_at TIMESTAMP NULL,
    deviation_meters FLOAT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (driver_user_id) REFERENCES user_company_access(user_id),
    FOREIGN KEY (route_id) REFERENCES routes(id),
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    INDEX idx_route (route_id),
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_status (status),
    INDEX idx_driver (driver_user_id)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 012
-- ============================================================================