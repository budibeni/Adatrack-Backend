-- ============================================================================
-- Migration: COMPANY 010 — notifications (Sent Notification History)
-- ============================================================================
-- Audit trail untuk semua notifikasi yang dikirim via email/SMS/websocket.
-- ============================================================================

CREATE TABLE IF NOT EXISTS notifications (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id BIGINT NOT NULL,                         -- references user_company_access.user_id
    alert_id BIGINT,                                 -- nullable; bila terkait alert tertentu
    company_code VARCHAR(20) NOT NULL,                -- denormalisasi per-tenant
    channel ENUM('websocket','email','sms','push') NOT NULL,
    alert_type VARCHAR(50),                          -- e.g. 'geofence','speed','sos','offline','battery'
    subject VARCHAR(255),                            -- email subject / SMS prefix
    body TEXT,                                        -- message body (rendered template)
    status ENUM('pending','sent','delivered','failed','skipped') DEFAULT 'pending',
    provider_response JSON,                            -- response dari provider (SMS gateway / SMTP server)
    error_message TEXT,                                -- error bila gagal
    retry_count INT DEFAULT 0,
    sent_at TIMESTAMP NULL,                            -- ketika berhasil dikirim
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_user (user_id),
    INDEX idx_alert (alert_id),
    INDEX idx_company (company_code),
    INDEX idx_status (status),
    INDEX idx_created_at (created_at DESC),
    INDEX idx_channel_status (channel, status)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 010
-- ============================================================================