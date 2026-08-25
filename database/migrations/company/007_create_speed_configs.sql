-- ============================================================================
-- Migration: COMPANY 007 — speed_configs
-- ============================================================================

CREATE TABLE IF NOT EXISTS speed_configs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT NULL,                          -- NULL = global default
    speed_limit_kmh FLOAT NOT NULL,
    grace_margin_kmh FLOAT DEFAULT 5.0,              -- toleransi sebelum alert
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE,
    INDEX idx_vehicle (vehicle_id),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 007
-- ============================================================================