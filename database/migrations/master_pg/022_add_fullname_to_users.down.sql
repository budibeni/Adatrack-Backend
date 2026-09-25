SET search_path TO adatrack_gps_master;
ALTER TABLE tm_users DROP COLUMN IF EXISTS full_name;
ALTER TABLE tm_users_b2c DROP COLUMN IF EXISTS full_name;
