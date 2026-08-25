-- ============================================================================
-- Migration: MASTER 010 — vehicle_types (Master Reference Data, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_types (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    category_id BIGINT NOT NULL,
    code VARCHAR(30) NOT NULL,
    name VARCHAR(100) NOT NULL,
    name_local VARCHAR(100) NOT NULL,
    description TEXT,
    typical_gvw_kg DECIMAL(10,2),
    fuel_types TEXT, -- setara SET(...) MySQL: CSV string, divalidasi aplikasi
    is_active BOOLEAN DEFAULT TRUE,
    display_order INT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_vtype_code UNIQUE (code),
    CONSTRAINT fk_vtype_category FOREIGN KEY (category_id) REFERENCES vehicle_categories(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_vtype_category ON vehicle_types (category_id);
CREATE INDEX IF NOT EXISTS idx_vtype_code ON vehicle_types (code);
CREATE INDEX IF NOT EXISTS idx_vtype_active ON vehicle_types (is_active);
CREATE INDEX IF NOT EXISTS idx_vtype_order ON vehicle_types (display_order);

-- Seed ringkas: type per kategori utama (lengkap dapat disinkronkan dari
-- varian MySQL via tool genregions/vehicle_ref — lihat README).
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code='PVB'), 'SEDAN', 'Sedan', 'Sedan', '4-door passenger sedan', 1800, '{petrol,diesel,hybrid,electric}', 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code='PVB'), 'SUV',   'SUV',   'SUV',   'Sport utility vehicle', 2000, '{petrol,diesel,hybrid,electric}', 20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code='LCV'), 'VAN',   'Van',   'Van',   'Cargo/passenger van', 2500, '{petrol,diesel,electric}', 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code='LCV'), 'PICKUP_TRUCK', 'Pickup Truck', 'Truk Pickup', 'Pickup', 3000, '{petrol,diesel}', 20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code='HCV'), 'HEAVY_TRUCK', 'Heavy Truck', 'Truk Berat', 'Heavy truck', 15000, '{diesel}', 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code='TW'),  'MOTORCYCLE', 'Motorcycle', 'Sepeda Motor', 'Two-wheeler', 300, '{petrol,electric}', 10, TRUE)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    name_local = EXCLUDED.name_local,
    description = EXCLUDED.description,
    typical_gvw_kg = EXCLUDED.typical_gvw_kg,
    fuel_types = EXCLUDED.fuel_types,
    display_order = EXCLUDED.display_order,
    is_active = EXCLUDED.is_active;

-- ============================================================================
-- End of MASTER 010 (postgres)
-- ============================================================================