ALTER TABLE tm_routes
ADD COLUMN IF NOT EXISTS planned_path_geojson JSONB;
