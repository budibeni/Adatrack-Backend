-- Template schema for company (will be executed dynamically per company)
-- Usage: adatrack_gps_{code}
-- Since this is init script, we just create a default one for tests

CREATE SCHEMA IF NOT EXISTS adatrack_gps_default;
SET search_path TO adatrack_gps_default;

CREATE TABLE IF NOT EXISTS th_telemetry_logs (
    id SERIAL PRIMARY KEY,
    imei VARCHAR(20) NOT NULL,
    latitude DOUBLE PRECISION NOT NULL,
    longitude DOUBLE PRECISION NOT NULL,
    speed DOUBLE PRECISION NOT NULL,
    acc BOOLEAN NOT NULL,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
