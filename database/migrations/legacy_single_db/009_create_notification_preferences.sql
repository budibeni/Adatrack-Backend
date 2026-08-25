-- ============================================================================
-- Migration: 009_create_notification_preferences_table
-- ============================================================================
-- User notification preferences for alerts.
-- Configures per-user / per-alert-type / per-vehicle channel preferences
-- (WebSocket / email / SMS) — granularitas notifikasi B3.
--
-- Historis: perubahan granularitas dari migrasi 011 (add 'ALL' enum,
-- vehicle_id, FK vehicles, idx_notif_prefs_vehicle) telah digabungkan
-- ke CREATE TABLE ini sehingga tabel dibentuk langsung dalam state final.
-- Migrasi 011 dihapus karena tidak lagi diperlukan.
-- ============================================================================

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id BIGINT UNSIGNED NOT NULL,
    -- vehicle_id NULL = berlaku untuk seluruh kendaraan milik user (global).
    -- vehicle_id terisi = preferensi khusus kendaraan tersebut saja.
    vehicle_id BIGINT UNSIGNED NULL,
    -- 'ALL' berlaku untuk semua tipe alert (catch-all fallback).
    alert_type ENUM('GEOFENCE', 'SPEED', 'SOS', 'BATTERY', 'OFFLINE', 'ALL') NOT NULL,
    channel ENUM('WEBSOCKET', 'EMAIL', 'SMS') NOT NULL,
    is_enabled TINYINT UNSIGNED NOT NULL DEFAULT 1,
    -- Channel-specific settings
    email_address VARCHAR(191) NULL,
    sms_phone VARCHAR(30) NULL,
    -- Timing
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    -- PK: satu baris per (user_id, alert_type) → satu channel + satu vehicle scope.
    --    (global vehicle_id=NULL OR spesifik vehicle_id=X, tidak keduanya untuk
    --     alert_type yang sama; bila diperlukan granularitas ganda gunakan alert_type 'ALL'.)
    PRIMARY KEY (user_id, alert_type),
    -- Index lookup by alert_type (legacy, tetap dipertahankan).
    INDEX idx_notif_prefs_alert_type (alert_type),
    -- Index optimised untuk lookup recipient: vehicle → user → channel → enabled.
    INDEX idx_notif_prefs_vehicle (vehicle_id, alert_type, channel, is_enabled),
    CONSTRAINT fk_notif_prefs_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_notif_prefs_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of Migration 009
-- ============================================================================