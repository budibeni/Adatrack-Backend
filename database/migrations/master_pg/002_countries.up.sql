SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_countries (
    code VARCHAR(10) PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);
