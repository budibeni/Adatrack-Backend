SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_menus (
    id SERIAL PRIMARY KEY,
    module_id INT REFERENCES tm_modules(id),
    code VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    path VARCHAR(200) NOT NULL,
    parent_id INT REFERENCES tm_menus(id),
    sort_order INT DEFAULT 0,
    enabled BOOLEAN DEFAULT true
);
