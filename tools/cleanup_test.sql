-- Clean up dummy vehicles and their telemetry
DELETE FROM adatrack_gps_master.tm_vehicles WHERE imei LIKE '8600000000000%';
-- Assuming telemetry was stored in tenant schema if they matched a tenant, or discarded if not.
-- Wait, if they didn't match a tenant, they were discarded by ingestion-tcp!
-- Let's check ingestion-tcp logic for unregistered vehicles.
