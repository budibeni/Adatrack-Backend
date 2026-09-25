SET search_path TO adatrack_gps_master, public;
CREATE EXTENSION IF NOT EXISTS postgis SCHEMA public;
CREATE TABLE IF NOT EXISTS tm_regions (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    level VARCHAR(50) NOT NULL, -- 'province', 'city', 'district', 'village'
    parent_id INT REFERENCES tm_regions(id),
    geom public.geometry(MultiPolygon, 4326)
);

CREATE INDEX IF NOT EXISTS idx_tm_regions_geom ON tm_regions USING GIST (geom);
