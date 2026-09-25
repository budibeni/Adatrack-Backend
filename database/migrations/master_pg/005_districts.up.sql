SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_districts (
    id SERIAL PRIMARY KEY,
    city_id INT REFERENCES tm_cities(id),
    name VARCHAR(100) NOT NULL
);
