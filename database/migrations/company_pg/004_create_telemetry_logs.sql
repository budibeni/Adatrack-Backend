-- ============================================================================
-- Migration: COMPANY 004 — telemetry_logs (Partitioned per Month, PostgreSQL)
-- ============================================================================
-- Mitra PostgreSQL dari migration 004 MySQL (RANGE TO_DAYS partitioning).
-- Postgres native partitioning: PARTITION BY RANGE (timestamp) + partitions
-- dengan batas eksplisit + SATU default partition (setara p_future MAXVALUE
-- untuk menampung timestamp di luar rentang yang dibuat).
--
-- Catatan deviasi tetap berlaku (tanpa FK & SPATIAL index pada tabel append-only).
--
-- IMPORTANT: partition key (timestamp) HARUS menjadi bagian dari setiap
-- unique/PK index pada partitioned table -> PK (id, timestamp), sama dgn MySQL.
-- `timestamp` dianggap UTC (TIMESTAMP WITHOUT TIME ZONE) konsisten
-- dengan parseTime/UTC app.
-- ============================================================================

CREATE TABLE IF NOT EXISTS telemetry_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    speed DOUBLE PRECISION DEFAULT 0,
    heading DOUBLE PRECISION DEFAULT 0,
    altitude DOUBLE PRECISION DEFAULT 0,
    acc_status SMALLINT DEFAULT 0,
    battery_level INT DEFAULT 0,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

CREATE TABLE IF NOT EXISTS telemetry_logs_p2026_q1 PARTITION OF telemetry_logs
    FOR VALUES FROM ('2026-01-01') TO ('2026-04-01');
CREATE TABLE IF NOT EXISTS telemetry_logs_p2026_q2 PARTITION OF telemetry_logs
    FOR VALUES FROM ('2026-04-01') TO ('2026-07-01');
CREATE TABLE IF NOT EXISTS telemetry_logs_p2026_q3 PARTITION OF telemetry_logs
    FOR VALUES FROM ('2026-07-01') TO ('2026-10-01');
CREATE TABLE IF NOT EXISTS telemetry_logs_p2026_q4 PARTITION OF telemetry_logs
    FOR VALUES FROM ('2026-10-01') TO ('2027-01-01');
CREATE TABLE IF NOT EXISTS telemetry_logs_p2027_q1 PARTITION OF telemetry_logs
    FOR VALUES FROM ('2027-01-01') TO ('2027-04-01');
-- Default partition menyerap data di luar rentang di atas (setara p_future).
CREATE TABLE IF NOT EXISTS telemetry_logs_p_default PARTITION OF telemetry_logs DEFAULT;

-- Index pada parent table otomatis terbawa ke partitions.
CREATE INDEX IF NOT EXISTS idx_tl_vehicle_time ON telemetry_logs (vehicle_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tl_imei_time ON telemetry_logs (imei, timestamp DESC);

-- ============================================================================
-- End of COMPANY 004 (postgres)
-- ============================================================================