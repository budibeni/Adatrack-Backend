-- ============================================================================
-- Migration: COMPANY 011 — routes
-- ============================================================================
-- Manajemen rute untuk penegakan disiplin driver (PRD §6.2 / B3).
-- ============================================================================

CREATE TABLE IF NOT EXISTS routes (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    waypoints JSON NOT NULL,                          -- array of {lat, lon}
    estimated_duration_sec INT,
    created_by BIGINT NOT NULL,                        -- references user_company_access.user_id
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by) REFERENCES user_company_access(user_id),
    INDEX idx_active (is_active),
    INDEX idx_created_by (created_by)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 011
-- ============================================================================