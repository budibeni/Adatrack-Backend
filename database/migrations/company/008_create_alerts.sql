-- ============================================================================
-- Migration: COMPANY 008 — alerts
-- ============================================================================
-- Lifecycle: open → acknowledged → resolved. acknowledged_by mereferensi
-- user_company_access.user_id (controled FK, nullable, ON DELETE SET NULL).
-- ============================================================================

CREATE TABLE IF NOT EXISTS alerts (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    alert_type ENUM('GEOFENCE_BREACH', 'OVERSPEEDING', 'OFFLINE', 'BATTERY_LOW', 'SOS', 'ROUTE_DEVIATION') NOT NULL,
    severity ENUM('low', 'medium', 'high', 'critical') DEFAULT 'medium',
    description TEXT,
    status ENUM('open', 'acknowledged', 'resolved') DEFAULT 'open',
    acknowledged_by BIGINT,                          -- references user_company_access.user_id (NO FK, nullable)
    acknowledged_at TIMESTAMP NULL,
    resolved_at TIMESTAMP NULL,
    vehicle_lat DECIMAL(10, 8),
    vehicle_lon DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    FOREIGN KEY (acknowledged_by) REFERENCES user_company_access(user_id) ON DELETE SET NULL,
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_status (status),
    INDEX idx_alert_type (alert_type),
    INDEX idx_created_at (created_at DESC),
    INDEX idx_severity (severity)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 008
-- ============================================================================