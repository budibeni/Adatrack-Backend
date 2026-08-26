-- ============================================================================
-- Migration: COMPANY 014 — fuel_configs (B5a, PRD v1.3.0 Module 7)
-- ============================================================================
-- Threshold deteksi FUEL_DROP / REFUEL per-vehicle. `vehicle_id NULL` =
-- default global company; baris per-vehicle MENANG atas global (pola
-- speed_configs, migration 007).
--
-- drop_threshold / refuel_threshold dalam satuan yang SAMA dengan fuel_level
-- (cm/liter/%). Delta dihitung antar pembacaan berturut-turut dalam window.
-- ============================================================================

CREATE TABLE IF NOT EXISTS fuel_configs (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    vehicle_id BIGINT DEFAULT NULL,
    drop_threshold DOUBLE NOT NULL DEFAULT 10.0,
    refuel_threshold DOUBLE NOT NULL DEFAULT 10.0,
    window_seconds INT NOT NULL DEFAULT 300,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uq_vehicle (vehicle_id),
    INDEX idx_vehicle_enabled (vehicle_id, enabled)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 014
-- ============================================================================