-- ============================================================================
-- Migration: COMPANY 009 — notification_preferences
-- ============================================================================
-- Konfigurasi notifikasi per user per tipe alert per channel.
-- Channel: websocket (real-time push), email (SMTP), sms (SMS gateway).
-- ============================================================================

CREATE TABLE IF NOT EXISTS notification_preferences (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                         -- references user_company_access.user_id
    alert_type VARCHAR(50) NOT NULL,                 -- e.g. 'geofence', 'speed', 'sos', 'offline', 'battery'
    channel ENUM('websocket', 'email', 'sms', 'push') NOT NULL,
    min_severity ENUM('info', 'warning', 'critical') DEFAULT 'warning', -- minimum severity untuk channel ini
    delivery_config JSON,                            -- channel-specific config: {"email": "override@corp.com", "phone_number": "+62xxx"} bila berbeda dari profil user
    is_enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    UNIQUE KEY unique_user_alert_channel (user_id, alert_type, channel),
    INDEX idx_user (user_id),
    INDEX idx_enabled (is_enabled),
    INDEX idx_severity (min_severity)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 009
-- ============================================================================