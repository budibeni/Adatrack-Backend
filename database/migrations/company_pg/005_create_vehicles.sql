-- ============================================================================
-- Migration: COMPANY 005 — tm_vehicles (fleet master, PRD §6.2)
-- ============================================================================
-- Note: odometer_km / engine_hours (B7.1) and trip columns (B7.2) arrive as
-- their own additive migrations in phase B7 — this migration only creates the
-- identity/classification/compliance/physical spec/live-state fields B0–B2 need.
-- category/type codes reference master.tm_vehicle_categories/_types LOGICALLY.

CREATE TABLE IF NOT EXISTS tm_vehicles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- identity
    imei VARCHAR(30) NOT NULL,
    plate_number VARCHAR(20) NOT NULL,
    make VARCHAR(50),
    model VARCHAR(50),
    variant VARCHAR(50),
    year_of_manufacture INT,
    engine_number VARCHAR(50),
    chassis_number VARCHAR(50),
    vin VARCHAR(50),
    color VARCHAR(30),
    fuel_type VARCHAR(20) CHECK (fuel_type IN ('petrol', 'diesel', 'electric', 'hybrid', 'gas')),

    -- classification
    vehicle_category_code VARCHAR(20),
    vehicle_type_code VARCHAR(40),

    -- compliance
    registration_number VARCHAR(50),
    registration_expiry DATE,
    insurance_number VARCHAR(50),
    insurance_expiry DATE,
    road_tax_expiry DATE,
    inspection_expiry DATE,

    -- physical specs
    gross_vehicle_weight INT,
    payload_capacity INT,
    vehicle_length INT,
    vehicle_width INT,
    vehicle_height INT,

    -- driver / device
    driver_user_id BIGINT,
    driver_name VARCHAR(100),
    device_model VARCHAR(50),
    firmware_version VARCHAR(50),

    -- denormalized live state (updated by the B2 read path / workers)
    last_seen_at TIMESTAMPTZ,
    current_lat DECIMAL(10, 8),
    current_lon DECIMAL(11, 8),
    current_speed DOUBLE PRECISION,

    status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'inactive', 'maintenance')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_vehicles_imei UNIQUE (imei),
    CONSTRAINT uq_tm_vehicles_chassis UNIQUE (chassis_number)
);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_status ON tm_vehicles (status);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_category ON tm_vehicles (vehicle_category_code);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_type ON tm_vehicles (vehicle_type_code);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_driver ON tm_vehicles (driver_user_id);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_last_seen ON tm_vehicles (last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_deleted ON tm_vehicles (deleted_at);