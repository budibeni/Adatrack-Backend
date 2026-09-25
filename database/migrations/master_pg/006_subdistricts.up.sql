SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_subdistricts (
    id SERIAL PRIMARY KEY,
    district_id INT REFERENCES tm_districts(id),
    name VARCHAR(100) NOT NULL
);
