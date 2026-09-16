SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_cities (
    id SERIAL PRIMARY KEY,
    province_id INT REFERENCES tm_provinces(id),
    name VARCHAR(100) NOT NULL
);
