SET search_path TO adatrack_gps_master;
ALTER TABLE tm_users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255);
ALTER TABLE tm_users_b2c ADD COLUMN IF NOT EXISTS full_name VARCHAR(255);
