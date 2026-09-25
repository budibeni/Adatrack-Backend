CREATE TABLE IF NOT EXISTS th_route_assignments (
    id BIGSERIAL PRIMARY KEY,
    route_id INT NOT NULL REFERENCES tm_routes(id),
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id),
    driver_user_id INT,
    status VARCHAR(20) DEFAULT 'assigned',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
