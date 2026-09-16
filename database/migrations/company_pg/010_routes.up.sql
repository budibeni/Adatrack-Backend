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
