-- We cannot easily re-add NOT NULL if there are NULLs, but we can set a default
UPDATE th_telemetry_logs SET lat = 0 WHERE lat IS NULL;
UPDATE th_telemetry_logs SET lon = 0 WHERE lon IS NULL;
ALTER TABLE th_telemetry_logs ALTER COLUMN lat SET NOT NULL;
ALTER TABLE th_telemetry_logs ALTER COLUMN lon SET NOT NULL;
