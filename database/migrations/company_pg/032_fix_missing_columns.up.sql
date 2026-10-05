-- Fix missing columns on Staging due to retroactively edited migrations

-- tm_drivers missing columns (from 020_phase_b12_enterprise)
ALTER TABLE tm_drivers 
ADD COLUMN IF NOT EXISTS ktp_number VARCHAR(100),
ADD COLUMN IF NOT EXISTS place_of_birth VARCHAR(100),
ADD COLUMN IF NOT EXISTS date_of_birth DATE,
ADD COLUMN IF NOT EXISTS address TEXT,
ADD COLUMN IF NOT EXISTS placement VARCHAR(100),
ADD COLUMN IF NOT EXISTS join_date DATE;

-- tm_geofences missing columns (from 005_geofences)
ALTER TABLE tm_geofences
ADD COLUMN IF NOT EXISTS group_id INT,
ADD COLUMN IF NOT EXISTS description TEXT,
ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'active';

-- tm_routes missing columns (from 010_routes)
ALTER TABLE tm_routes
ADD COLUMN IF NOT EXISTS group_id INT,
ADD COLUMN IF NOT EXISTS description TEXT,
ADD COLUMN IF NOT EXISTS planned_distance FLOAT,
ADD COLUMN IF NOT EXISTS estimated_duration FLOAT;
