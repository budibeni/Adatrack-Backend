-- ============================================================================
-- Migration: 006_create_alerts_table
-- ============================================================================
-- Alerts table storing detected alerts (geofence, speed, SOS, battery, offline)
-- Lifecycle: OPEN -> ACKNOWLEDGED -> RESOLVED
-- ============================================================================

CREATE TABLE IF NOT EXISTS alerts (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    alert_type ENUM('GEOFENCE', 'SPEED', 'SOS', 'BATTERY', 'OFFLINE') NOT NULL,
    severity ENUM('LOW', 'MEDIUM', 'HIGH', 'CRITICAL') NOT NULL DEFAULT 'HIGH',
    imei VARCHAR(15) NOT NULL,
    vehicle_id BIGINT UNSIGNED NULL,
    user_id BIGINT UNSIGNED NULL,
    -- Geofence-specific fields
    geofence_id BIGINT UNSIGNED NULL,
    geofence_name VARCHAR(191) NULL,
    -- Speed-specific fields
    speed_limit DECIMAL(5, 2) NULL,
    speed_observed DECIMAL(5, 2) NULL,
    -- SOS-specific fields
    sos_type ENUM('BUTTON', 'PROTOCOL') NULL,
    -- Status & lifecycle
    status ENUM('OPEN', 'ACKNOWLEDGED', 'RESOLVED') NOT NULL DEFAULT 'OPEN',
    ack_at TIMESTAMP NULL,
    ack_by BIGINT UNSIGNED NULL,
    resolved_at TIMESTAMP NULL,
    resolved_by BIGINT UNSIGNED NULL,
    -- Metadata
    tta_seconds INT UNSIGNED NULL, -- Time to Acknowledge
    -- Automatic escalation (B3: "eskalasi otomatis bila tak di-ACK")
    escalation_count INT UNSIGNED NOT NULL DEFAULT 0, -- berapa kali auto-escalated
    last_escalated_at TIMESTAMP NULL, -- kapan escalation terakhir dikirim
    triggered_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    -- Metadata
    metadata JSON NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP NULL,
    PRIMARY KEY (id),
    INDEX idx_alerts_imei_status (imei, status),
    INDEX idx_alerts_type_severity (alert_type, severity),
    INDEX idx_alerts_ack_status (status, ack_at),
    INDEX idx_alerts_imei_type (imei, alert_type),
    INDEX idx_alerts_vehicle_id (vehicle_id),
    INDEX idx_alerts_deleted_at (deleted_at),
    INDEX idx_alerts_geofence_id (geofence_id),
    INDEX idx_alerts_sos_escalation (alert_type, status, triggered_at)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 006
-- ============================================================================