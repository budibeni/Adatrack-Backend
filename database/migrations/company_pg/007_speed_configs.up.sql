CREATE TABLE IF NOT EXISTS tm_speed_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    max_speed_kmh FLOAT NOT NULL,
    grace_margin_percent FLOAT DEFAULT 0,
    alert_severity VARCHAR(20) DEFAULT 'medium',
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
