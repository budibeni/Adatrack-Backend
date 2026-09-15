-- ============================================================================
-- Migration: COMPANY 007 — th_telemetry_logs (partitioned monthly, PRD §6.2/§FR-3.2)
-- ============================================================================
-- Transaksi Header (prefix `th_` per §6.0) that receives the batched inserts of
-- worker-persistence (PRD FR-3.1/FR-3.4).
--   * RANGE partition per MONTH → fast playback queries, cheap purge, less lock
--     contention (FR-3.2). A DEFAULT partition catches out-of-range timestamps so
--     an insert can never fail because a partition is missing.
--   * Partition key (timestamp) MUST be part of every unique index → PK (id, timestamp).
--   * `timestamp` is stored in UTC (TIMESTAMPTZ) to match the pipeline.
--   * No FK / no soft delete here: this is a high-volume history table governed by
--     the retention/partition policy (§11), not by user delete actions (§6.0.1).
-- ============================================================================

CREATE TABLE IF NOT EXISTS th_telemetry_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    latitude DECIMAL(10, 8) NOT NULL,
    longitude DECIMAL(11, 8) NOT NULL,
    speed DOUBLE PRECISION NOT NULL DEFAULT 0,
    heading DOUBLE PRECISION NOT NULL DEFAULT 0,
    altitude DOUBLE PRECISION NOT NULL DEFAULT 0,
    acc_status SMALLINT NOT NULL DEFAULT 0,
    battery_level INT NOT NULL DEFAULT 0,
    "timestamp" TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, "timestamp")
) PARTITION BY RANGE ("timestamp");

-- Monthly partitions for the current + next calendar year (idempotent), plus a
-- DEFAULT partition so inserts outside the pre-created range never fail.
DO $$
DECLARE
    start_month DATE := date_trunc('month', CURRENT_DATE)::date - INTERVAL '6 months';
    m DATE;
    part TEXT;
    months INT;
BEGIN
    FOR months IN 0..23 LOOP
        m := (start_month + (months || ' months')::interval)::date;
        part := 'th_telemetry_logs_p' || to_char(m, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_telemetry_logs FOR VALUES FROM (%L) TO (%L)',
            part, m, (m + INTERVAL '1 month')::date);
    END LOOP;
END $$;

CREATE TABLE IF NOT EXISTS th_telemetry_logs_pdefault PARTITION OF th_telemetry_logs DEFAULT;

-- Indexes on the partitioned parent propagate to every partition.
CREATE INDEX IF NOT EXISTS idx_th_telemetry_logs_vehicle_time ON th_telemetry_logs (vehicle_id, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_th_telemetry_logs_imei_time ON th_telemetry_logs (imei, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_th_telemetry_logs_company_time ON th_telemetry_logs (company_code, "timestamp" DESC);

-- Helper for future partition maintenance (B4 monitoring job calls it per month).
-- Created in the tenant schema so each company owns its own maintenance helper.
CREATE OR REPLACE FUNCTION tm_ensure_telemetry_partition(target_date DATE)
RETURNS TEXT AS $$
DECLARE
    m DATE := date_trunc('month', target_date)::date;
    part TEXT := 'th_telemetry_logs_p' || to_char(m, 'YYYYMM');
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_telemetry_logs FOR VALUES FROM (%L) TO (%L)',
        part, m, (m + INTERVAL '1 month')::date);
    RETURN part;
END;
$$ LANGUAGE plpgsql;