-- ============================================================================
-- Migration: COMPANY 036 — Monthly Partitioning for High-Volume Tables
-- Tables: th_telemetry_logs, th_fuel_logs
-- Range: 36 Months (2026-01-01 to 2028-12-31) + Auto-generator function
-- ============================================================================

DO $$
DECLARE
    cur_month DATE := date_trunc('month', CURRENT_DATE)::date;
    target_month DATE := (cur_month + (36 || ' months')::interval)::date;
    m DATE := DATE '2026-01-01';
    part_telem TEXT;
    part_fuel TEXT;
    has_telem_default BOOLEAN;
    has_fuel_default BOOLEAN;
BEGIN
    -- 1. Detach default partitions if they exist to prevent constraint violations
    SELECT EXISTS (
        SELECT 1 FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'th_telemetry_logs' AND c.relname = 'th_telemetry_logs_default'
    ) INTO has_telem_default;

    IF has_telem_default THEN
        ALTER TABLE th_telemetry_logs DETACH PARTITION th_telemetry_logs_default;
    END IF;

    SELECT EXISTS (
        SELECT 1 FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'th_fuel_logs' AND c.relname = 'th_fuel_logs_default'
    ) INTO has_fuel_default;

    IF has_fuel_default THEN
        ALTER TABLE th_fuel_logs DETACH PARTITION th_fuel_logs_default;
    END IF;

    -- 2. Create monthly partitions for th_telemetry_logs and th_fuel_logs
    WHILE m <= target_month LOOP
        part_telem := 'th_telemetry_logs_p' || to_char(m, 'YYYYMM');
        part_fuel := 'th_fuel_logs_p' || to_char(m, 'YYYYMM');
        
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_telemetry_logs FOR VALUES FROM (%L) TO (%L)',
            part_telem, m, (m + INTERVAL '1 month')::date
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_fuel_logs FOR VALUES FROM (%L) TO (%L)',
            part_fuel, m, (m + INTERVAL '1 month')::date
        );
        m := (m + INTERVAL '1 month')::date;
    END LOOP;

    -- 3. Re-route data from detached default tables into th_telemetry_logs parent
    IF has_telem_default THEN
        INSERT INTO th_telemetry_logs SELECT * FROM th_telemetry_logs_default;
        TRUNCATE TABLE th_telemetry_logs_default;
        ALTER TABLE th_telemetry_logs ATTACH PARTITION th_telemetry_logs_default DEFAULT;
    ELSE
        CREATE TABLE IF NOT EXISTS th_telemetry_logs_default PARTITION OF th_telemetry_logs DEFAULT;
    END IF;

    IF has_fuel_default THEN
        INSERT INTO th_fuel_logs SELECT * FROM th_fuel_logs_default;
        TRUNCATE TABLE th_fuel_logs_default;
        ALTER TABLE th_fuel_logs ATTACH PARTITION th_fuel_logs_default DEFAULT;
    ELSE
        CREATE TABLE IF NOT EXISTS th_fuel_logs_default PARTITION OF th_fuel_logs DEFAULT;
    END IF;
END $$;

-- Automated partition maintenance function for future months / year changes
CREATE OR REPLACE FUNCTION tm_ensure_monthly_partitions(months_ahead INT DEFAULT 12)
RETURNS void AS $$
DECLARE
    cur_month DATE := date_trunc('month', CURRENT_DATE)::date;
    target_month DATE := (cur_month + (months_ahead || ' months')::interval)::date;
    m DATE := cur_month - INTERVAL '1 month';
    part_telem TEXT;
    part_fuel TEXT;
BEGIN
    WHILE m <= target_month LOOP
        part_telem := 'th_telemetry_logs_p' || to_char(m, 'YYYYMM');
        part_fuel := 'th_fuel_logs_p' || to_char(m, 'YYYYMM');
        
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_telemetry_logs FOR VALUES FROM (%L) TO (%L)',
            part_telem, m, (m + INTERVAL '1 month')::date
        );
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF th_fuel_logs FOR VALUES FROM (%L) TO (%L)',
            part_fuel, m, (m + INTERVAL '1 month')::date
        );
        m := (m + INTERVAL '1 month')::date;
    END LOOP;
END;
$$ LANGUAGE plpgsql;
