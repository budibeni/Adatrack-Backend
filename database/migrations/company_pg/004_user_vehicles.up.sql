CREATE TABLE IF NOT EXISTS tm_user_vehicles (
    user_id INT NOT NULL,
    vehicle_id INT NOT NULL REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    PRIMARY KEY(user_id, vehicle_id)
);
