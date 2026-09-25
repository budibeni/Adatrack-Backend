CREATE TABLE IF NOT EXISTS tm_groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    parent_id INT REFERENCES tm_groups(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS tm_group_vehicles (
    group_id INT REFERENCES tm_groups(id),
    vehicle_id INT REFERENCES tm_vehicles(id),
    PRIMARY KEY(group_id, vehicle_id)
);

CREATE TABLE IF NOT EXISTS tm_drivers (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    phone VARCHAR(20),
    email VARCHAR(100),
    license_number VARCHAR(100),
    license_type VARCHAR(50),
    license_expiry DATE,
    rfid_tag VARCHAR(100),
    group_id INT REFERENCES tm_groups(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS tm_driver_vehicles (
    driver_id INT REFERENCES tm_drivers(id),
    vehicle_id INT REFERENCES tm_vehicles(id),
    assigned_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    unassigned_at TIMESTAMP WITH TIME ZONE,
    PRIMARY KEY (driver_id, vehicle_id, assigned_at)
);

CREATE TABLE IF NOT EXISTS tm_rfid_cards (
    id SERIAL PRIMARY KEY,
    card_number VARCHAR(100) UNIQUE NOT NULL,
    assigned_to_type VARCHAR(50),
    assigned_to_id INT,
    status VARCHAR(50) DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS th_access_logs (
    id BIGSERIAL PRIMARY KEY,
    card_number VARCHAR(100),
    reader_device_imei VARCHAR(100),
    access_time TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    status VARCHAR(50),
    location_lat DOUBLE PRECISION,
    location_lon DOUBLE PRECISION
);

CREATE TABLE IF NOT EXISTS tm_assets (
    id SERIAL PRIMARY KEY,
    asset_code VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    category VARCHAR(50),
    status VARCHAR(50) DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS th_incidents (
    id BIGSERIAL PRIMARY KEY,
    vehicle_id INT REFERENCES tm_vehicles(id),
    driver_id INT REFERENCES tm_drivers(id),
    incident_time TIMESTAMP WITH TIME ZONE NOT NULL,
    incident_type VARCHAR(100) NOT NULL,
    severity VARCHAR(50),
    description TEXT,
    location_lat DOUBLE PRECISION,
    location_lon DOUBLE PRECISION,
    status VARCHAR(50) DEFAULT 'open',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS tm_organizations (
    id SERIAL PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    parent_id INT REFERENCES tm_organizations(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS tm_integrations (
    id SERIAL PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    url TEXT,
    token VARCHAR(255),
    secret VARCHAR(255),
    status VARCHAR(50) DEFAULT 'active',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE IF NOT EXISTS tm_tenant_settings (
    id SERIAL PRIMARY KEY,
    key VARCHAR(100) UNIQUE NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tm_shared_locations (
    id SERIAL PRIMARY KEY,
    token VARCHAR(100) UNIQUE NOT NULL,
    vehicle_id INT REFERENCES tm_vehicles(id),
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_by INT,
    deleted_at TIMESTAMP WITH TIME ZONE
);
