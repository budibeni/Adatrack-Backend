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
