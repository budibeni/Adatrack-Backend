-- ============================================================================
-- Migration: COMPANY 013 — fuel_logs (Partitioned per Month, PostgreSQL)
-- ============================================================================
-- Mitra PostgreSQL dari migration 013 MySQL (B5a, PRD v1.3.0 Module 7).
-- Postgres native partitioning: PARTITION BY RANGE (timestamp) + default
-- partition (setara p_future MAXVALUE).
--
-- Tanpa FK pada tabel append-only ter-partisi — pola migration 004_pg.
-- ============================================================================

CREATE TABLE IF NOT EXISTS fuel_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    fuel_level DOUBLE PRECISION DEFAULT NULL,
    fuel_volume DOUBLE PRECISION DEFAULT NULL,
    fuel_temp_c DOUBLE PRECISION DEFAULT NULL,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

CREATE TABLE IF NOT EXISTS fuel_logs_p2026_q3 PARTITION OF fuel_logs
    FOR VALUES FROM ('2026-07-01') TO ('2026-10-01');
CREATE TABLE IF NOT EXISTS fuel_logs_p2026_q4 PARTITION OF fuel_logs
    FOR VALUES FROM ('2026-10-01') TO ('2027-01-01');
CREATE TABLE IF NOT EXISTS fuel_logs_p2027_q1 PARTITION OF fuel_logs
    FOR VALUES FROM ('2027-01-01') TO ('2027-04-01');
CREATE TABLE IF NOT EXISTS fuel_logs_p_default PARTITION OF fuel_logs DEFAULT;

CREATE INDEX IF NOT EXISTS idx_fl_vehicle_time ON fuel_logs (vehicle_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_fl_imei_time ON fuel_logs (imei, timestamp DESC);

-- ============================================================================
-- End of COMPANY 013 (postgres)
-- ===========================================================================