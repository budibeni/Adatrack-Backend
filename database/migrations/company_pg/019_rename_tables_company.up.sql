-- Idempotent rename for legacy tables if they exist
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='vehicles') THEN
        ALTER TABLE vehicles RENAME TO tm_vehicles;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='telemetry_logs') THEN
        ALTER TABLE telemetry_logs RENAME TO th_telemetry_logs;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='fuel_logs') THEN
        ALTER TABLE fuel_logs RENAME TO th_fuel_logs;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='alerts') THEN
        ALTER TABLE alerts RENAME TO th_alerts;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='vehicle_trips') THEN
        ALTER TABLE vehicle_trips RENAME TO th_vehicle_trips;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='media_events') THEN
        ALTER TABLE media_events RENAME TO th_media_events;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname=current_schema() AND tablename='geofences') THEN
        ALTER TABLE geofences RENAME TO tm_geofences;
    END IF;
END $$;
