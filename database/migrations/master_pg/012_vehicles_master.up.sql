SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_vehicle_categories (
    code VARCHAR(20) PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);
CREATE TABLE IF NOT EXISTS tm_vehicle_types (
    code VARCHAR(20) PRIMARY KEY,
    category_code VARCHAR(20) REFERENCES tm_vehicle_categories(code),
    name VARCHAR(100) NOT NULL
);
CREATE TABLE IF NOT EXISTS tm_vehicle_imei_map (
    imei VARCHAR(20) PRIMARY KEY,
    company_code VARCHAR(50) NOT NULL REFERENCES tm_companies(code),
    vehicle_id INT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_imei_map_company ON tm_vehicle_imei_map(company_code);
