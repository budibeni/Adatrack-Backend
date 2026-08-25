-- ============================================================================
-- Migration: COMPANY 004 — telemetry_logs (Partitioned per Month)
-- ============================================================================
-- Partisi bulanan via TO_DAYS(timestamp) (PRD §6.2). p_future = partisi default;
-- partisi bulanan baru ditambahkan via ALTER TABLE setiap bulan.
--
-- DEVIASI-PRD (documented): 
--   1) SPATIAL INDEX di telemetry_logs ter-partisi → ditolak MySQL 8.0
--      (error 1178 "storage engine doesn't support GEOMETRY").
--   2) FOREIGN KEY vehicle_id → ditolak MySQL 8.0 pada partitioned table
--      (error 1506 "Foreign keys are not yet supported in conjunction with
--      partitioning"). Referensi FK sengaja dihilangkan di tabel partisi
--      append-only ini (integritas dijaga aplikasi via master vehicle_imei_map
--      + company DB vehicles); pola ini sama dgn batch-partition production.
--   Keputusan:
--     * telemetry_logs tetap ter-partisi (wajib utk retention + query 30 hari).
--     * Bounding-box geofence memakai WHERE lat/lon BETWEEN + index
--       (vehicle_id, timestamp)/(imei, timestamp); data terbaru dari Redis
--       live-state vehicle:state:<IMEI>.
-- ============================================================================

CREATE TABLE IF NOT EXISTS telemetry_logs (
    id BIGINT AUTO_INCREMENT,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,               -- denormalisasi untuk safety
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    speed FLOAT DEFAULT 0,
    heading FLOAT DEFAULT 0,
    altitude FLOAT DEFAULT 0,
    acc_status TINYINT(1) DEFAULT 0,
    battery_level INT DEFAULT 0,
    timestamp DATETIME NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp),
    INDEX idx_vehicle_time (vehicle_id, timestamp DESC),
    INDEX idx_imei_time (imei, timestamp DESC)
    -- (Tanpa FOREIGN KEY & SPATIAL INDEX — lihat keterangan DEVIASI-PRD.)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci
PARTITION BY RANGE (TO_DAYS(timestamp)) (
    PARTITION p_2025_Q4 VALUES LESS THAN (TO_DAYS('2026-01-01')),
    PARTITION p_2026_Q1 VALUES LESS THAN (TO_DAYS('2026-04-01')),
    PARTITION p_2026_Q2 VALUES LESS THAN (TO_DAYS('2026-07-01')),
    PARTITION p_2026_Q3 VALUES LESS THAN (TO_DAYS('2026-10-01')),
    PARTITION p_2026_Q4 VALUES LESS THAN (TO_DAYS('2027-01-01')),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);

-- ============================================================================
-- End of COMPANY 004
-- ============================================================================