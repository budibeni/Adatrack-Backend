CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_schema_migrations (
    version VARCHAR(50) PRIMARY KEY,
    checksum VARCHAR(100) NOT NULL,
    applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    duration_ms INT,
    applied_by VARCHAR(100)
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_countries (
    code VARCHAR(10) PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_provinces (
    id SERIAL PRIMARY KEY,
    country_code VARCHAR(10) REFERENCES tm_countries(code),
    name VARCHAR(100) NOT NULL
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_cities (
    id SERIAL PRIMARY KEY,
    province_id INT REFERENCES tm_provinces(id),
    name VARCHAR(100) NOT NULL
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_districts (
    id SERIAL PRIMARY KEY,
    city_id INT REFERENCES tm_cities(id),
    name VARCHAR(100) NOT NULL
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_subdistricts (
    id SERIAL PRIMARY KEY,
    district_id INT REFERENCES tm_districts(id),
    name VARCHAR(100) NOT NULL
);
SET search_path TO adatrack_gps_master;
DO $$ BEGIN CREATE TYPE business_type_enum AS ENUM('b2b', 'b2c'); EXCEPTION WHEN duplicate_object THEN null; END $$;
CREATE TABLE IF NOT EXISTS tm_companies (
    code VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    legal_name VARCHAR(100),
    tax_id VARCHAR(50),
    country_code VARCHAR(10) REFERENCES tm_countries(code),
    timezone VARCHAR(50) DEFAULT 'UTC',
    address TEXT,
    phone VARCHAR(20),
    business_type business_type_enum NOT NULL DEFAULT 'b2b',
    created_by INT,
    updated_by INT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    global_role VARCHAR(50) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    must_change_password BOOLEAN DEFAULT false,
    password_changed_at TIMESTAMP WITH TIME ZONE,
    last_login_at TIMESTAMP WITH TIME ZONE,
    deleted_at TIMESTAMP WITH TIME ZONE,
    deleted_by INT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_users_b2c (
    id SERIAL PRIMARY KEY,
    email VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL,
    phone VARCHAR(20),
    is_active BOOLEAN DEFAULT true,
    must_change_password BOOLEAN DEFAULT false,
    last_login_at TIMESTAMP WITH TIME ZONE,
    deleted_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
SET search_path TO adatrack_gps_master;
DO $$ BEGIN CREATE TYPE app_type_enum AS ENUM('business', 'personal'); EXCEPTION WHEN duplicate_object THEN null; END $$;
CREATE TABLE IF NOT EXISTS tm_modules (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    app app_type_enum NOT NULL,
    sort_order INT DEFAULT 0,
    enabled BOOLEAN DEFAULT true
);
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
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_company_media_config (
    id SERIAL PRIMARY KEY,
    company_code VARCHAR(50) NOT NULL REFERENCES tm_companies(code),
    bucket VARCHAR(100) NOT NULL,
    retention_days INT DEFAULT 30,
    max_file_mb INT DEFAULT 50,
    hmac_secret VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS tm_audit_logs (
    id BIGSERIAL PRIMARY KEY,
    action VARCHAR(100) NOT NULL,
    outcome VARCHAR(50) NOT NULL,
    actor_user_id INT,
    actor_email VARCHAR(100),
    actor_role VARCHAR(50),
    company_code VARCHAR(50),
    entity_type VARCHAR(50),
    entity_id VARCHAR(50),
    before_data JSONB,
    after_data JSONB,
    ip_address VARCHAR(50),
    user_agent TEXT,
    request_id VARCHAR(100),
    reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_company ON tm_audit_logs(company_code, created_at);
