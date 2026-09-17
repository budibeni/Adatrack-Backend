CREATE TABLE IF NOT EXISTS tm_fuel_configs (
    id SERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    max_volume_liters FLOAT NOT NULL,
    drop_threshold_liters FLOAT NOT NULL,
    refuel_threshold_liters FLOAT NOT NULL,
    enabled BOOLEAN DEFAULT true,
    deleted_at TIMESTAMP WITH TIME ZONE
);
