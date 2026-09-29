ALTER TABLE tm_vehicles 
ADD COLUMN IF NOT EXISTS vehicle_name VARCHAR(100),
ADD COLUMN IF NOT EXISTS category VARCHAR(50),
ADD COLUMN IF NOT EXISTS year INT,
ADD COLUMN IF NOT EXISTS fuel_type VARCHAR(50),
ADD COLUMN IF NOT EXISTS color VARCHAR(50),
ADD COLUMN IF NOT EXISTS fuel_capacity FLOAT,
ADD COLUMN IF NOT EXISTS stnk_expiry DATE,
ADD COLUMN IF NOT EXISTS kir_expiry DATE,
ADD COLUMN IF NOT EXISTS notes TEXT,
ADD COLUMN IF NOT EXISTS gps_install_date DATE;

-- Migrate existing data
UPDATE tm_vehicles SET category = model WHERE category IS NULL;
UPDATE tm_vehicles SET vehicle_name = make WHERE vehicle_name IS NULL;
