-- ============================================================================
-- Migration: COMPANY 001 — user_company_access (LOCAL registry per-company)
-- ============================================================================
-- user_id mereferensi master.users.id (NO cross-DB FK).
-- Berisi user khas company ini termasuk admin masing-masing.
-- Role/permissions di-override per company via role_override (PRD §6.2).
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_company_access (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                        -- references master.users.id (NO cross-DB FK)
    role_override VARCHAR(20),                      -- NULL = pakai global_role dari master.users
    is_active BOOLEAN DEFAULT TRUE,                 -- bisa non-aktif per company
    permissions JSON,                               -- menu-specific overrides
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY unique_user (user_id),
    INDEX idx_user (user_id),
    INDEX idx_role_override (role_override),
    INDEX idx_active (is_active)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 001
-- ============================================================================