CREATE SCHEMA IF NOT EXISTS adatrack_gps_default;
SET search_path TO adatrack_gps_default;

CREATE TABLE IF NOT EXISTS tm_user_company_access (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL,
    role_override VARCHAR(50),
    is_active BOOLEAN DEFAULT true,
    permissions JSONB,
    deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_role_menu_access (
    id SERIAL PRIMARY KEY,
    role VARCHAR(50) NOT NULL,
    menu_id INT NOT NULL,
    can_view BOOLEAN DEFAULT true,
    can_create BOOLEAN DEFAULT false,
    can_edit BOOLEAN DEFAULT false,
    can_delete BOOLEAN DEFAULT false,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_vehicles (
    id SERIAL PRIMARY KEY,
    imei VARCHAR(20) UNIQUE NOT NULL,
    plate_number VARCHAR(20),
    make VARCHAR(50),
    model VARCHAR(50),
    status VARCHAR(20) DEFAULT 'active',
    last_seen_at TIMESTAMP,
    current_lat DOUBLE PRECISION,
    current_lon DOUBLE PRECISION,
    current_speed FLOAT,
    odometer_km FLOAT DEFAULT 0,
    engine_hours FLOAT DEFAULT 0,
    deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_user_vehicles (
    user_id INT NOT NULL,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    PRIMARY KEY(user_id, vehicle_id)
);

CREATE TABLE IF NOT EXISTS th_telemetry_logs (
    id BIGSERIAL,
    vehicle_id INT NOT NULL,
    imei VARCHAR(20) NOT NULL,
    company_code VARCHAR(50) NOT NULL,
    lat DECIMAL(10, 7) NOT NULL,
    lon DECIMAL(10, 7) NOT NULL,
    speed FLOAT NOT NULL,
    heading FLOAT,
    altitude FLOAT,
    acc_status SMALLINT NOT NULL,
    battery_level FLOAT,
    timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(id, timestamp)
) PARTITION BY RANGE (timestamp);
-- Note: In production we will script partition creation.
