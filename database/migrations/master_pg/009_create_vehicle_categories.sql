-- ============================================================================
-- Migration: MASTER 009 — vehicle_categories (Master Reference Data, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_categories (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code VARCHAR(20) NOT NULL,
    name VARCHAR(100) NOT NULL,
    name_local VARCHAR(100) NOT NULL,
    description TEXT,
    min_gvw_kg DECIMAL(10,2),
    max_gvw_kg DECIMAL(10,2),
    display_order INT DEFAULT 0,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_vcat_code UNIQUE (code)
);
CREATE INDEX IF NOT EXISTS idx_vcat_code ON vehicle_categories (code);
CREATE INDEX IF NOT EXISTS idx_vcat_active ON vehicle_categories (is_active);
CREATE INDEX IF NOT EXISTS idx_vcat_order ON vehicle_categories (display_order);

-- Seed: Standard Enterprise Vehicle Categories (idempotent via ON CONFLICT).
INSERT INTO vehicle_categories (code, name, name_local, description, min_gvw_kg, max_gvw_kg, display_order, is_active) VALUES
    ('PVB', 'Passenger Vehicle',        'Kendaraan Penumpang',          'Cars & SUVs for personal/family transport',          NULL,   3500,    10, TRUE),
    ('LCV', 'Light Commercial Vehicle', 'Kendaraan Komersial Ringan',   'Light commercial vehicles up to 3.5 t GVW',           100,   3500,    20, TRUE),
    ('MCV', 'Medium Commercial Vehicle','Kendaraan Komersial Sedang',   'Medium commercial vehicles 3.5-12 t GVW',            3500,  12000,    30, TRUE),
    ('HCV', 'Heavy Commercial Vehicle', 'Kendaraan Komersial Berat',    'Heavy commercial vehicles over 12 t GVW',            12000,   NULL,    40, TRUE),
    ('TW',  'Two-Wheeler',              'Sepeda Motor',                  'Motorcycles, scooters, mopeds',                       NULL,   NULL,    50, TRUE),
    ('THW', 'Three-Wheeler',            'Tiga Roda',                     'Auto-rickshaws, tricycles',                          NULL,   NULL,    55, TRUE),
    ('EV',  'Electric Vehicle',         'Kendaraan Listrik',             'Electric-propulsion vehicles (any class)',            NULL,   NULL,    60, TRUE),
    ('SPV', 'Special Purpose Vehicle',  'Kendaraan Khusus',              'Fire trucks, ambulances, excavators, etc.',            NULL,   NULL,    70, TRUE)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    name_local = EXCLUDED.name_local,
    description = EXCLUDED.description,
    min_gvw_kg = EXCLUDED.min_gvw_kg,
    max_gvw_kg = EXCLUDED.max_gvw_kg,
    display_order = EXCLUDED.display_order,
    is_active = EXCLUDED.is_active;

-- ============================================================================
-- End of MASTER 009 (postgres)
-- ============================================================================