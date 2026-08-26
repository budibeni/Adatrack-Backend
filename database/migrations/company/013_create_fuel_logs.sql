-- ============================================================================
-- Migration: COMPANY 013 — fuel_logs (B5a, PRD v1.3.0 Module 7)
-- ============================================================================
-- Partisi bulanan via TO_DAYS(timestamp) — pola telemetry_logs (migration 004).
--
-- DEVIASI-PRD (documented, sama dgn migration 004):
--   FOREIGN KEY vehicle_id → ditolak MySQL 8.0 pada partitioned table
--   (error 1506). Referensi FK sengaja dihilangkan; integritas dijaga
--   aplikasi (worker-persistence routing per company + master vehicle_imei_map).
--
-- fuel_level disimpan apa adanya dari sensor (cm utk GT06 !AIOIL / liter atau
-- persen utk Teltonika IO). Satuan ditentukan oleh konfigurasi perangkat;
-- alert FUEL_DROP/REFUEL membandingkan delta antar pembacaan, bukan absolut.
-- ============================================================================

CREATE TABLE IF NOT EXISTS fuel_logs (
    id BIGINT AUTO_INCREMENT,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    fuel_level DOUBLE DEFAULT NULL,
    fuel_volume DOUBLE DEFAULT NULL,
    fuel_temp_c DOUBLE DEFAULT NULL,
    timestamp DATETIME NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp),
    INDEX idx_vehicle_time (vehicle_id, timestamp DESC),
    INDEX idx_imei_time (imei, timestamp DESC)
    -- (Tanpa FOREIGN KEY — lihat keterangan DEVIASI-PRD.)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci
PARTITION BY RANGE (TO_DAYS(timestamp)) (
    PARTITION p_2026_Q3 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p_2026_Q4 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);

-- ============================================================================
-- End of COMPANY 013
-- ============================================================================