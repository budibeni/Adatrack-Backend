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
