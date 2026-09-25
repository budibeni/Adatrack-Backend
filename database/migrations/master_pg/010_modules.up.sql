SET search_path TO adatrack_gps_master;
DO $$ BEGIN CREATE TYPE app_type_enum AS ENUM('business', 'personal'); EXCEPTION WHEN duplicate_object THEN null; END $$;
CREATE TABLE IF NOT EXISTS tm_modules (
    id SERIAL PRIMARY KEY,
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(100) NOT NULL,
    app app_type_enum NOT NULL,
    sort_order INT DEFAULT 0,
    enabled BOOLEAN DEFAULT true
);
