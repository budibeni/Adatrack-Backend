ALTER TABLE tm_groups ADD COLUMN IF NOT EXISTS group_type VARCHAR(50) DEFAULT 'vehicle';

CREATE TABLE IF NOT EXISTS tm_group_geofences (
    group_id INT REFERENCES tm_groups(id) ON DELETE CASCADE,
    geofence_id INT REFERENCES tm_geofences(id) ON DELETE CASCADE,
    PRIMARY KEY(group_id, geofence_id)
);

CREATE TABLE IF NOT EXISTS tm_group_routes (
    group_id INT REFERENCES tm_groups(id) ON DELETE CASCADE,
    route_id INT REFERENCES tm_routes(id) ON DELETE CASCADE,
    PRIMARY KEY(group_id, route_id)
);
