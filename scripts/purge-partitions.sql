-- Run this via pg_cron or periodic script
DO $$ 
DECLARE 
    r RECORD;
    cutoff_date TIMESTAMP := NOW() - INTERVAL '30 days'; -- 30 days retention policy
BEGIN
    -- Example for dynamic partition drop (very simplified, usually requires finding partition names based on rules)
    FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname LIKE 'adatrack_gps_%' AND tablename LIKE 'th_telemetry_logs_p%') LOOP
        -- Extract date from partition name (assuming format th_telemetry_logs_pYYYYMM)
        -- If older than cutoff, DROP TABLE
        -- (Pseudo code implementation here)
        RAISE NOTICE 'Evaluating partition %', r.tablename;
    END LOOP;
END $$;
