CREATE TABLE IF NOT EXISTS tm_geofence_vehicles (
    geofence_id INT REFERENCES tm_geofences(id) ON DELETE CASCADE,
    vehicle_id INT REFERENCES tm_vehicles(id) ON DELETE CASCADE,
    enabled BOOLEAN DEFAULT true,
    PRIMARY KEY(geofence_id, vehicle_id)
);
