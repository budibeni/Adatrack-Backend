CREATE TABLE IF NOT EXISTS tm_geofences (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    area_type VARCHAR(20) NOT NULL,
    coordinates JSONB NOT NULL,
    radius_meters FLOAT,
    boundary_points JSONB,
    created_by INT,
    deleted_at TIMESTAMP WITH TIME ZONE
);
