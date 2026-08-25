-- ============================================================================
-- Migration: MASTER 008 — vehicle_categories (Master Reference Data)
-- ============================================================================
-- Master lookup table for vehicle categories — enterprise standard.
-- Dipakai di semua company DB via vehicle_category_code (denormalisasi) dan
-- sebagai taxonomi global untuk filter, reporting, & rate-limiting per segmen.
--
-- Enterprise-standard categories (ISO 377-2019 / UNECE aligned + adatrack mgmt):
--   PVB  — Passenger Vehicle       (cars, SUVs, hatchbacks for personal/family use)
--   LCV  — Light Commercial Vehicle (≤ 3.5 t GVW: van, pickup, small trucks)
--   MCV  — Medium Commercial Vehicle (3.5 – 12 t GVW: medium trucks)
--   HCV  — Heavy Commercial Vehicle  (> 12 t GVW: heavy trucks, buses)
--   TW   — Two-Wheeler             (motorcycles, scooters, mopeds)
--   THW  — Three-Wheeler           (auto-rickshaws, tricycles)
--   EV   — Electric Vehicle        (any vehicle class but electric propulsion)
--   SPV  — Special Purpose Vehicle (fire trucks, ambulances, excavators, etc.)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_categories (
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    code         VARCHAR(20)  UNIQUE NOT NULL,        -- e.g. "PVB", "LCV", "HCV", "TW", "SPV"
    name         VARCHAR(100) NOT NULL,              -- e.g. "Passenger Vehicle"
    name_local   VARCHAR(100) NOT NULL,              -- e.g. "Kendaraan Penumpang" (Bahasa Indonesia)
    description  TEXT,
    min_gvw_kg   DECIMAL(10,2),                      -- Gross Vehicle Weight minimum (kg); NULL = no lower bound
    max_gvw_kg   DECIMAL(10,2),                      -- GVW maximum (kg); NULL = no upper bound
    display_order INT DEFAULT 0,                     -- urutan tampilan di UI dropdown
    is_active    BOOLEAN DEFAULT TRUE,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_code         (code),
    INDEX idx_active       (is_active),
    INDEX idx_display_order (display_order)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Seed: Standard Enterprise Vehicle Categories
-- Idempotent: gunakan INSERT ... ON DUPLICATE KEY UPDATE
-- ============================================================================
INSERT INTO vehicle_categories (code, name, name_local, description, min_gvw_kg, max_gvw_kg, display_order, is_active) VALUES
    ('PVB',  'Passenger Vehicle',        'Kendaraan Penumpang',          'Cars & SUVs for personal/family transport',          NULL,    3500,  10, TRUE),
    ('LCV',  'Light Commercial Vehicle', 'Kendaraan Komersial Ringan',   'Light commercial vehicles up to 3.5 t GVW',           100,   3500,  20, TRUE),
    ('MCV',  'Medium Commercial Vehicle','Kendaraan Komersial Sedang',   'Medium commercial vehicles 3.5–12 t GVW',           3500,  12000, 30, TRUE),
    ('HCV',  'Heavy Commercial Vehicle', 'Kendaraan Komersial Berat',    'Heavy commercial vehicles over 12 t GVW',            12000,     NULL, 40, TRUE),
    ('TW',   'Two-Wheeler',              'Sepeda Motor',                  'Motorcycles, scooters, mopeds',                       NULL,    NULL,  50, TRUE),
    ('THW',  'Three-Wheeler',            'Tiga Roda',                     'Auto-rickshaws, tricycles',                          NULL,    NULL,  55, TRUE),
    ('EV',   'Electric Vehicle',         'Kendaraan Listrik',             'Electric-propulsion vehicles (any class)',            NULL,    NULL,  60, TRUE),
    ('SPV',  'Special Purpose Vehicle',  'Kendaraan Khusus',              'Fire trucks, ambulances, excavators, etc.',            NULL,    NULL,  70, TRUE)
ON DUPLICATE KEY UPDATE
    name         = VALUES(name),
    name_local   = VALUES(name_local),
    description  = VALUES(description),
    min_gvw_kg   = VALUES(min_gvw_kg),
    max_gvw_kg   = VALUES(max_gvw_kg),
    display_order = VALUES(display_order),
    is_active    = VALUES(is_active);

-- ============================================================================
-- End of MASTER 008
-- ============================================================================