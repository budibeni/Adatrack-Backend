-- ============================================================================
-- Migration: COMPANY 014 — td_fuel_logs (B5a, PRD §6.2 / Module 7 FR-7.4)
-- ============================================================================
-- Fuel sensor readings persisted by worker-persistence. Partitioned monthly
-- (same pattern as th_telemetry_logs, migration 007) so playback queries and
-- purge by month stay cheap under the 5000-device target.
--
-- A fuel-only packet (no GPS fix) is written HERE — not to th_telemetry_logs
-- (worker-persistence, FR-3.4). Position + fuel packets also write here.
-- No FK / no soft-delete: high-volume history governed by retention (§11).

CREATE TABLE IF NOT EXISTS td_fuel_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    fuel_level DOUBLE PRECISION,
    fuel_volume DOUBLE PRECISION,
    fuel_temp_c DOUBLE PRECISION,
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    acc_status SMALLINT,
    "timestamp" TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, "timestamp")
) PARTITION BY RANGE ("timestamp");

-- Monthly partitions for the current + next year, plus a DEFAULT catch-all
-- (same idempotent pattern as th_telemetry_logs).
DO $$
DECLARE
    start_month DATE := date_trunc('month', CURRENT_DATE)::date - INTERVAL '6 months';
    m DATE;
    part TEXT;
    months INT;
BEGIN
    FOR months IN 0..23 LOOP
        m := (start_month + (months || ' months')::interval)::date;
        part := 'td_fuel_logs_p' || to_char(m, 'YYYYMM');
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF td_fuel_logs FOR VALUES FROM (%L) TO (%L)',
            part, m, (m + INTERVAL '1 month')::date);
    END LOOP;
END $$;

CREATE TABLE IF NOT EXISTS td_fuel_logs_pdefault PARTITION OF td_fuel_logs DEFAULT;

CREATE INDEX IF NOT EXISTS idx_td_fuel_logs_vehicle_time ON td_fuel_logs (vehicle_id, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_td_fuel_logs_imei_time ON td_fuel_logs (imei, "timestamp" DESC);
CREATE INDEX IF NOT EXISTS idx_td_fuel_logs_company_time ON td_fuel_logs (company_code, "timestamp" DESC);
