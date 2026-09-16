SET search_path TO adatrack_gps_master;

CREATE TABLE IF NOT EXISTS tm_countries (
    id SERIAL PRIMARY KEY,
    code VARCHAR(10) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL
);

CREATE TABLE IF NOT EXISTS tm_companies (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    legal_name VARCHAR(100),
    tax_id VARCHAR(50),
    country_code VARCHAR(10) REFERENCES tm_countries(code),
    timezone VARCHAR(50) DEFAULT 'UTC',
    address TEXT,
    phone VARCHAR(20),
    business_type VARCHAR(10) NOT NULL DEFAULT 'b2b',
    created_by INT,
    updated_by INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    global_role VARCHAR(50) NOT NULL,
    is_active BOOLEAN DEFAULT true,
    must_change_password BOOLEAN DEFAULT false,
    password_changed_at TIMESTAMP,
    last_login_at TIMESTAMP,
    deleted_at TIMESTAMP,
    deleted_by INT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_users_b2c (
    id SERIAL PRIMARY KEY,
    email VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL,
    phone VARCHAR(20),
    is_active BOOLEAN DEFAULT true,
    must_change_password BOOLEAN DEFAULT false,
    last_login_at TIMESTAMP,
    deleted_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_modules (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    app VARCHAR(20) NOT NULL,
    sort_order INT DEFAULT 0,
    enabled BOOLEAN DEFAULT true
);

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

CREATE TABLE IF NOT EXISTS tm_vehicle_imei_map (
    imei VARCHAR(20) PRIMARY KEY,
    company_code VARCHAR(50) NOT NULL REFERENCES tm_companies(code),
    vehicle_id INT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_audit_logs (
    id SERIAL PRIMARY KEY,
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
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_schema_migrations (
    version VARCHAR(50) PRIMARY KEY,
    checksum VARCHAR(100) NOT NULL,
    applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    duration_ms INT,
    applied_by VARCHAR(100)
);
