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
