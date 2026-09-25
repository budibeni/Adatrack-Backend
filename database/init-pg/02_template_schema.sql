CREATE TABLE IF NOT EXISTS tm_user_company_access (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL, 
    role_code VARCHAR(50),
    is_active BOOLEAN DEFAULT true,
    permissions JSONB,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_user_access_userid ON tm_user_company_access(user_id);
CREATE TABLE IF NOT EXISTS tm_role_menu_access (
    id SERIAL PRIMARY KEY,
    role_code VARCHAR(50) NOT NULL,
    menu_id INT NOT NULL, 
    can_view BOOLEAN DEFAULT true,
    can_create BOOLEAN DEFAULT false,
    can_edit BOOLEAN DEFAULT false,
    can_delete BOOLEAN DEFAULT false,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS tm_vehicles (
    id SERIAL PRIMARY KEY,
    imei VARCHAR(20) UNIQUE NOT NULL,
    plate_number VARCHAR(20),
    make VARCHAR(50),
    model VARCHAR(50),
    status VARCHAR(20) DEFAULT 'active',
    last_seen_at TIMESTAMP WITH TIME ZONE,
    current_lat DOUBLE PRECISION,
    current_lon DOUBLE PRECISION,
    current_speed FLOAT,
    odometer_km FLOAT DEFAULT 0,
    engine_hours FLOAT DEFAULT 0,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_vehicles_imei ON tm_vehicles(imei);
CREATE INDEX IF NOT EXISTS idx_vehicles_status ON tm_vehicles(status);
CREATE TABLE IF NOT EXISTS tm_user_vehicles (
    user_id INT NOT NULL,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    PRIMARY KEY(user_id, vehicle_id)
);
CREATE TABLE IF NOT EXISTS tm_geofences (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    area_type VARCHAR(20) NOT NULL,
    coordinates JSONB NOT NULL,
    radius_meters FLOAT,
    boundary_points JSONB,
    created_by INT,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS tm_geofence_vehicles (
    geofence_id INT REFERENCES tm_geofences(id) ON DELETE CASCADE,
    vehicle_id INT REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    enabled BOOLEAN DEFAULT true,
    PRIMARY KEY(geofence_id, vehicle_id)
);
CREATE TABLE IF NOT EXISTS tm_speed_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    max_speed_kmh FLOAT NOT NULL,
    grace_margin_percent FLOAT DEFAULT 0,
    alert_severity VARCHAR(20) DEFAULT 'medium',
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS tm_fuel_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    max_volume_liters FLOAT NOT NULL,
    drop_threshold_liters FLOAT NOT NULL,
    refuel_threshold_liters FLOAT NOT NULL,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS tm_notification_preferences (
    id SERIAL PRIMARY KEY,
    user_id INT NOT NULL,
    alert_type VARCHAR(50) NOT NULL,
    channel VARCHAR(20) NOT NULL,
    enabled BOOLEAN DEFAULT true,
    min_severity VARCHAR(20) DEFAULT 'low'
);
CREATE TABLE IF NOT EXISTS tm_routes (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    waypoints JSONB NOT NULL,
    driver_user_id INT,
    vehicle_id INT REFERENCES tm_vehicles(id),
    status VARCHAR(20) DEFAULT 'active',
    deviation_threshold_meters FLOAT DEFAULT 100,
    deleted_at TIMESTAMP WITH TIME ZONE
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
    satellites INT DEFAULT 0,
    gsm_signal INT DEFAULT 0,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(id, timestamp)
) PARTITION BY RANGE (timestamp);
CREATE TABLE IF NOT EXISTS th_telemetry_logs_default PARTITION OF th_telemetry_logs DEFAULT;
CREATE INDEX IF NOT EXISTS idx_telemetry_vehicle_ts ON th_telemetry_logs(vehicle_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_imei_ts ON th_telemetry_logs(imei, timestamp DESC);
CREATE TABLE IF NOT EXISTS th_fuel_logs (
    id BIGSERIAL,
    vehicle_id INT NOT NULL,
    fuel_level FLOAT,
    volume_liters FLOAT,
    temperature_c FLOAT,
    lat DECIMAL(10, 7),
    lon DECIMAL(10, 7),
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(id, timestamp)
) PARTITION BY RANGE (timestamp);
CREATE TABLE IF NOT EXISTS th_fuel_logs_default PARTITION OF th_fuel_logs DEFAULT;
CREATE TABLE IF NOT EXISTS th_alerts (
    id BIGSERIAL PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    vehicle_id INT NOT NULL,
    lat DECIMAL(10, 7),
    lon DECIMAL(10, 7),
    metadata JSONB,
    status VARCHAR(20) DEFAULT 'open',
    acknowledged_by INT,
    resolved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS th_route_assignments (
    id BIGSERIAL PRIMARY KEY,
    route_id INT NOT NULL REFERENCES tm_routes(id),
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id),
    driver_user_id INT,
    status VARCHAR(20) DEFAULT 'assigned',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS th_media_events (
    id BIGSERIAL PRIMARY KEY,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id),
    status VARCHAR(20) DEFAULT 'uploaded',
    storage_key VARCHAR(255) NOT NULL,
    content_type VARCHAR(50),
    media_type VARCHAR(20),
    size_bytes BIGINT,
    uploaded_at TIMESTAMP WITH TIME ZONE,
    expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE TABLE IF NOT EXISTS th_vehicle_trips (
    id BIGSERIAL PRIMARY KEY,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id),
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    start_lat DECIMAL(10, 7),
    start_lon DECIMAL(10, 7),
    end_lat DECIMAL(10, 7),
    end_lon DECIMAL(10, 7),
    distance_km FLOAT,
    max_speed FLOAT,
    avg_speed FLOAT,
    stop_count INT DEFAULT 0,
    duration_seconds INT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS td_vehicle_stops (
    id BIGSERIAL PRIMARY KEY,
    trip_id BIGINT NOT NULL REFERENCES th_vehicle_trips(id) ON DELETE CASCADE,
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE,
    duration_seconds INT,
    lat DECIMAL(10, 7),
    lon DECIMAL(10, 7),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS td_notifications (
    id BIGSERIAL PRIMARY KEY,
    alert_id BIGINT NOT NULL REFERENCES th_alerts(id) ON DELETE CASCADE,
    user_id INT NOT NULL,
    channel VARCHAR(20) NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    provider_response JSONB,
    error_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS th_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    action VARCHAR(100) NOT NULL,
    outcome VARCHAR(50) NOT NULL,
    actor_user_id INT,
    actor_email VARCHAR(100),
    actor_role VARCHAR(50),
    entity_type VARCHAR(50),
    entity_id VARCHAR(50),
    before_data JSONB,
    after_data JSONB,
    ip_address VARCHAR(50),
    user_agent TEXT,
    request_id VARCHAR(100),
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON th_audit_logs(actor_user_id, created_at);

CREATE OR REPLACE FUNCTION prevent_audit_update_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Audit logs are append-only. UPDATE and DELETE operations are not allowed.';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_prevent_audit_update
BEFORE UPDATE ON th_audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_update_delete();

CREATE TRIGGER trg_prevent_audit_delete
BEFORE DELETE ON th_audit_logs
FOR EACH ROW EXECUTE FUNCTION prevent_audit_update_delete();
