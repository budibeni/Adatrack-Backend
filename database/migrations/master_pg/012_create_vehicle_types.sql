-- ============================================================================
-- Migration: MASTER 012 — tm_vehicle_types (per-category reference)
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_vehicle_types (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_code VARCHAR(20) NOT NULL,
    code VARCHAR(40) NOT NULL,
    name VARCHAR(100) NOT NULL,
    sort_order INT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_tm_vehicle_types_code UNIQUE (code),
    CONSTRAINT fk_tm_vehicle_types_category FOREIGN KEY (category_code)
        REFERENCES tm_vehicle_categories (code)
);
CREATE INDEX IF NOT EXISTS idx_tm_vehicle_types_category ON tm_vehicle_types (category_code);

INSERT INTO tm_vehicle_types (category_code, code, name, sort_order) VALUES
    ('PVB', 'SEDAN',        'Sedan', 1),
    ('PVB', 'SUV',          'SUV', 2),
    ('PVB', 'MPV',          'MPV / Minibus', 3),
    ('LCV', 'PICKUP_TRUCK', 'Pickup Truck', 1),
    ('LCV', 'LIGHT_TRUCK',  'Light Truck', 2),
    ('LCV', 'VAN',          'Van', 3),
    ('HCV', 'MEDIUM_TRUCK', 'Medium Truck', 1),
    ('HCV', 'HEAVY_TRUCK',  'Heavy Truck', 2),
    ('HCV', 'TRAILER',      'Trailer', 3),
    ('TW',  'MOTORCYCLE',   'Sepeda Motor', 1),
    ('THW', 'TRICYCLE',     'Bajaj / Tricycle', 1),
    ('EV',  'EV_CAR',       'Mobil Listrik', 1),
    ('EV',  'EV_MOTORCYCLE','Motor Listrik', 2),
    ('SPV', 'AMBULANCE',    'Ambulans', 1),
    ('SPV', 'FIRE_TRUCK',   'Mobil Pemadam', 2),
    ('SPV', 'SPECIAL',      'Kendaraan Khusus Lainnya', 3)
ON CONFLICT (code) DO UPDATE SET
    category_code = EXCLUDED.category_code,
    name = EXCLUDED.name,
    sort_order = EXCLUDED.sort_order;