-- ============================================================================
-- Migration: MASTER 011 — audit_logs (Phase B4 hardening, GAP #12)
-- ============================================================================
-- Jejak audit keamanan tingkat master (lintas company):
--   - LOGIN_SUCCESS / LOGIN_FAILURE   : autentikasi
--   - ACCESS_DENIED                   : RBAC 403 (cross-tenant / row-level)
--   - TOKEN_REVOKED                   : logout / refresh-token diputar
--   - ADMIN_ACTION                    : perubahan konfigurasi/CRUD sensitif
--
-- Sengaja di MASTER DB: identitas global + event lintas tenant terpusat,
-- sedangkan data operasional tetap di company DB (PRD §6).
-- ============================================================================

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT UNSIGNED NULL,                -- NULL bila belum dikenal (mis. login gagal)
    company_code VARCHAR(20) NULL,
    event_type VARCHAR(50) NOT NULL,
    action VARCHAR(100) NOT NULL,
    entity VARCHAR(60) NULL,
    entity_id VARCHAR(64) NULL,
    ip_address VARCHAR(45) NULL,                 -- IPv6-ready
    user_agent VARCHAR(255) NULL,
    details JSON NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_audit_user (user_id, created_at),
    INDEX idx_audit_company_time (company_code, created_at),
    INDEX idx_audit_event_time (event_type, created_at)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 011
-- ============================================================================
