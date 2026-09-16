CREATE TABLE IF NOT EXISTS tm_fuel_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    drop_threshold_percent FLOAT NOT NULL,
    refuel_threshold_percent FLOAT NOT NULL,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
