SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_provinces (
    id SERIAL PRIMARY KEY,
    country_code VARCHAR(10) REFERENCES tm_countries(code),
    name VARCHAR(100) NOT NULL
);
