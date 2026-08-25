-- ============================================================================
-- Migration: MASTER 009 — vehicle_types (Master Reference Data)
-- ============================================================================
-- Master lookup table for vehicle types (sub-classes within a category).
-- Setiap type referential ke vehicle_categories via category_id (FK di MASTER DB).
-- Di company DB, vehicles.vehicle_type_code mereferensi code ini (denormalisasi,
-- tanpa cross-DB FK — konsisten dengan pola master.reference data).
--
-- Enterprise-standard types per category (ISO 377 / UNECE + adatrack-industry):
--   PVB  (Passenger Vehicle)    → Sedan, SUV, Hatchback, Coupe, Convertible, Station Wagon
--   LCV  (Light Commercial)      → Pickup Truck, Van, Minivan, Caravan, Light Truck, Chassis Cab
--   MCV  (Medium Commercial)     → Medium Truck, Box Truck, Flatbed Truck
--   HCV  (Heavy Commercial)      → Heavy Truck, Tractor Head, City Bus, Intercity Bus,
--                                   Double-Deck Bus, School Bus, Dump Truck
--   TW   (Two-Wheeler)           → Motorcycle, Scooter, Moped
--   THW  (Three-Wheeler)         → Auto Rickshaw, Tricycle
--   EV   (Electric Vehicle)      → Electric Car, Electric Bus, Electric Truck,
--                                   Electric Motorcycle, Electric Scooter, Electric Buggy
--   SPV  (Special Purpose)       → Fire Truck, Ambulance, Mobile Crane, Excavator,
--                                   Concrete Mixer, Water Tanker, Reefer Truck,
--                                   Street Sweeper, Boom Lift, Tow Truck
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_types (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    category_id     BIGINT NOT NULL,                     -- FK → vehicle_categories.id
    code            VARCHAR(30)  UNIQUE NOT NULL,        -- e.g. "SEDAN", "SUV", "PICKUP_TRUCK"
    name            VARCHAR(100) NOT NULL,               -- e.g. "Sedan", "Pickup Truck"
    name_local      VARCHAR(100) NOT NULL,               -- Bahasa Indonesia, e.g. "Sedan", "Truk Pickup"
    description     TEXT,
    typical_gvw_kg  DECIMAL(10,2),                      -- GVW contoh (kg) untuk type ini
    fuel_types      SET('petrol','diesel','electric','hybrid','CNG','LPG','hydrogen'),
    is_active       BOOLEAN DEFAULT TRUE,
    display_order   INT DEFAULT 0,                      -- urutan tampilan per kategori
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (category_id) REFERENCES vehicle_categories(id) ON DELETE RESTRICT,
    INDEX idx_category    (category_id),
    INDEX idx_code        (code),
    INDEX idx_active      (is_active),
    INDEX idx_display_order (display_order)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- Seed: Standard Enterprise Vehicle Types (per kategori)
-- Idempotent: gunakan INSERT ... ON DUPLICATE KEY UPDATE
-- ============================================================================
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
-- --- PVB: Passenger Vehicle ---
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'SEDAN',           'Sedan',                  'Sedan',                       '4-door passenger sedan',              1800, 'petrol,diesel,hybrid,electric', 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'SUV',             'Sport Utility Vehicle',  'Sport Utility Vehicle',       'Sport utility / 4x4 passenger',       2500, 'petrol,diesel,hybrid,electric', 20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'HATCHBACK',       'Hatchback',              'Hatchback',                   'Compact 3-door/5-door passenger',     1500, 'petrol,diesel,hybrid,electric', 30, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'COUPE',           'Coupe',                  'Coupe',                       '2-door sport passenger',              1700, 'petrol,diesel,hybrid',          40, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'CONVERTIBLE',     'Convertible',            'Convertible',                 'Soft-top / hard-top roadster',        1750, 'petrol,diesel,electric',        50, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'STATION_WAGON',   'Station Wagon',          'Station Wagon',               'Extended-roof passenger estate',      2000, 'petrol,diesel,hybrid,electric', 60, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'PVB'), 'PICKUP_CAR',      'Pickup Car',             'Pickup Car',                  'Car-based pickup (e.g. UTE)',         1900, 'petrol,diesel,hybrid',          70, TRUE)
ON DUPLICATE KEY UPDATE
    category_id    = VALUES(category_id),
    name           = VALUES(name),
    name_local     = VALUES(name_local),
    description    = VALUES(description),
    typical_gvw_kg = VALUES(typical_gvw_kg),
    fuel_types     = VALUES(fuel_types),
    is_active      = VALUES(is_active),
    display_order  = VALUES(display_order);

-- --- LCV: Light Commercial Vehicle ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'PICKUP_TRUCK',  'Pickup Truck',      'Truk Pickup',      'Open-bed light cargo truck',         3000, 'petrol,diesel,hybrid,electric', 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'VAN',           'Van',               'Van',              'Cargo/passenger van (e.g. HiAce)',   3200, 'petrol,diesel,electric',        20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'MINIVAN',       'Minivan',           'Minivan',          'MPV multi-purpose passenger van',  2800, 'petrol,diesel,hybrid,electric', 30, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'CARAVAN',       'Caravan',           'Karavana',         'Recreational travel trailer van',   2500, 'petrol,diesel',                 40, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'LIGHT_TRUCK',   'Light Truck',       'Truk Ringan',      'Small cargo truck <= 3.5 t GVW',    2500, 'petrol,diesel',                 50, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'LCV'), 'CHASSIS_LCV',   'Chassis Cab (LCV)', 'Chassis Cab',      'Bare chassis for body-mounting',     2200, 'diesel,electric',               60, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);


-- --- MCV: Medium Commercial Vehicle ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'MCV'), 'MEDIUM_TRUCK',   'Medium Truck',      'Truk Sedang',      'Cargo truck 3.5-12 t GVW',         8000, 'diesel,hybrid',                 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'MCV'), 'BOX_TRUCK',      'Box Truck',         'Truk Box',          'Enclosed cargo medium truck',       9000, 'diesel,hybrid',                 20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'MCV'), 'FLATBED_TRUCK',  'Flatbed Truck',     'Truk Bak Terbuka',  'Open flatbed medium truck',         8500, 'diesel',                        30, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);



-- --- HCV: Heavy Commercial Vehicle ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'HEAVY_TRUCK',     'Heavy Truck',       'Truk Berat',             'Cargo truck > 12 t GVW',            20000, 'diesel,hybrid',                 10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'TRACTOR_HEAD',    'Tractor Head',      'Traktor Roda',           'Articulated tractor unit',          18000, 'diesel,hybrid',                 20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'CITY_BUS',        'City Bus',          'Bus Kota',               'Urban passenger transit bus',       13000, 'diesel,electric,hybrid',        30, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'INTERCITY_BUS',   'Intercity Bus',     'Bus Interkota',          'Long-distance coach',               14000, 'diesel,electric,hybrid',        40, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'DOUBLE_DECK_BUS', 'Double-Deck Bus',   'Bus Double Decker',      'Two-storey passenger bus',          18000, 'diesel,electric,hybrid',        50, TRUE),
        ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'SCHOOL_BUS',      'School Bus',        'Bus Sekolah',            'Yellow school-transport bus',       13000, 'diesel,LPG',                    60, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'HCV'), 'DUMP_TRUCK',      'Dump Truck',        'Truk Gedung',            'Off-road construction dump truck',  25000, 'diesel',                        70, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);

-- --- TW: Two-Wheeler ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'TW'), 'MOTORCYCLE',  'Motorcycle',        'Sepeda Motor',             'Standard / cruiser motorcycle',          300, 'petrol,hybrid,electric',        10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'TW'), 'SCOOTER',     'Scooter',           'Skuter',                   'Step-through motor scooter',             200, 'petrol,electric',               20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'TW'), 'MOPED',       'Moped',             'Moped',                    'Low-power auxiliary bicycle',           100, 'petrol,electric',               30, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);

-- --- THW: Three-Wheeler ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'THW'), 'AUTO_RICKSHAW', 'Auto Rickshaw',    'Bajaj/Tuktuk',             'Motorized three-wheeler taxi',          450, 'petrol,electric,CNG',           10, TRUE),
        ((SELECT id FROM vehicle_categories WHERE code = 'THW'), 'TRICYCLE',      'Tricycle',         'Tricycle',                 'Cargo three-wheeler',                   600, 'petrol,electric',               20, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);

-- --- EV: Electric Vehicle ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_CAR',          'Electric Car',      'Mobil Listrik',            'Battery-electric passenger car',      2000, 'electric',                      10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_BUS',          'Electric Bus',      'Bus Listrik',              'Battery-electric transit bus',        13000, 'electric',                      20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_TRUCK',        'Electric Truck',    'Truk Listrik',             'Battery-electric cargo truck',        12000, 'electric',                      30, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_MOTORCYCLE',   'Electric Motorcycle', 'Sepeda Motor Listrik',     'Electric two-wheeler',                 300, 'electric',                      40, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_SCOOTER',      'Electric Scooter',  'Skuter Listrik',           'Electric step-through scooter',         200, 'electric',                      50, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'EV'), 'EV_BUGGY',        'Electric Buggy',    'Buggy Listrik',            'Low-speed electric utility vehicle',   800, 'electric',                      60, TRUE)
ON DUPLICATE KEY UPDATE
    display_order  = VALUES(display_order);

-- --- SPV: Special Purpose Vehicle ---
INSERT INTO vehicle_types (category_id, code, name, name_local, description, typical_gvw_kg, fuel_types, display_order, is_active) VALUES
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'FIRE_TRUCK',        'Fire Truck',          'Truk Pemadam',            'Firefighting apparatus',                18000, 'diesel',                        10, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'AMBULANCE',         'Ambulance',           'Ambulans',                'Medical emergency transport',             8000, 'diesel,petrol,hybrid',          20, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'MOBILE_CRANE',      'Mobile Crane',        'Mobile Crane',            'Hydraulic lifting crane truck',         25000, 'diesel',                        30, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'EXCAVATOR',         'Excavator',           'Exavator',                'Hydraulic excavator (tracked/wheeled)',  30000, 'diesel',                        40, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'CONCRETE_MIXER',    'Concrete Mixer',      'Truck Mixer',             'Rotating-drum concrete mixer truck',     22000, 'diesel',                        50, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'WATER_TANKER',      'Water Tanker',        'Truk Tangki Air',         'Water-carrying tanker truck',             16000, 'diesel',                        60, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'REFRIGERATOR_TRUCK','Refrigerator Truck', 'Truk Refrigerated',       'Reefer / cold-chain cargo truck',         15000, 'diesel,electric',               70, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'STREET_SWEEPER',    'Street Sweeper',      'Street Sweeper',          'Municipal road-sweeper truck',            14000, 'diesel,electric',               80, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'BOOM_LIFT',         'Boom Lift Truck',     'Boom Lift Truck',         'Aerial work platform truck',              18000, 'diesel',                        90, TRUE),
    ((SELECT id FROM vehicle_categories WHERE code = 'SPV'), 'TOW_TRUCK',         'Tow Truck',           'Truk Towing',             'Vehicle-recovery / wrecker truck',        15000, 'diesel',                       100, TRUE)
ON DUPLICATE KEY UPDATE
        display_order  = VALUES(display_order);

-- ============================================================================
-- End of MASTER 009
-- ============================================================================
