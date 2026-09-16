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
