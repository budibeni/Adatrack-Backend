CREATE TABLE IF NOT EXISTS tm_safety_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    max_speed_kmh FLOAT DEFAULT 80.0,
    harsh_accel_kph_s FLOAT DEFAULT 15.0,
    harsh_brake_kph_s FLOAT DEFAULT 15.0,
    hard_cornering_deg FLOAT DEFAULT 45.0,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);
