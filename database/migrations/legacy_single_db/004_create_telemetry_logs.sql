-- ============================================================================
-- Migration: 004_create_telemetry_logs_table
-- ============================================================================
-- Telemetry logs table - partitioned by range on received_at
-- Stores raw GPS telemetry data from devices
-- Partitioned every 3 months for 30-day query performance (PRD §12)
-- ============================================================================

CREATE TABLE IF NOT EXISTS telemetry_logs (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    imei VARCHAR(15) NOT NULL,
    received_at TIMESTAMP NOT NULL,
    event_timestamp TIMESTAMP NOT NULL,
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    speed DECIMAL(5, 2) NULL,
    heading SMALLINT NULL,
    satellites TINYINT UNSIGNED NULL,
    hdop DECIMAL(4, 2) NULL,
    battery_level TINYINT UNSIGNED NULL,
    input_message TEXT NULL,
    processed TINYINT UNSIGNED NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, received_at),
    INDEX idx_telemetry_imei_received (imei, received_at),
    INDEX idx_telemetry_event_ts (event_timestamp),
    INDEX idx_telemetry_processed (processed),
    INDEX idx_telemetry_imei_processed (imei, processed)
) ENGINE=InnoDB
-- PARTITION BY RANGE (UNIX_TIMESTAMP(received_at)) per kuartal.
-- Kolom received_at bertipe TIMESTAMP (timezone-dependent) => partition function
-- wajib UNIX_TIMESTAMP() (TO_DAYS() ditolak oleh MySQL utk TIMESTAMP; PRD §6.1
-- mencontohkan TO_DAYS pada DATETIME). Id partisi include received_at (PK).
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci
PARTITION BY RANGE (UNIX_TIMESTAMP(received_at)) (
    PARTITION p_2025_Q4 VALUES LESS THAN (UNIX_TIMESTAMP('2026-01-01 00:00:00')),
    PARTITION p_2026_Q1 VALUES LESS THAN (UNIX_TIMESTAMP('2026-04-01 00:00:00')),
    PARTITION p_2026_Q2 VALUES LESS THAN (UNIX_TIMESTAMP('2026-07-01 00:00:00')),
    PARTITION p_2026_Q3 VALUES LESS THAN (UNIX_TIMESTAMP('2026-10-01 00:00:00')),
    PARTITION p_2026_Q4 VALUES LESS THAN (UNIX_TIMESTAMP('2027-01-01 00:00:00')),
    PARTITION p_future VALUES LESS THAN MAXVALUE
);
-- ============================================================================
-- End of Migration 004
-- ============================================================================