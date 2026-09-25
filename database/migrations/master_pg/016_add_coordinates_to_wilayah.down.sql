SET search_path TO adatrack_gps_master;

DROP INDEX IF EXISTS idx_tm_cities_coords;
DROP INDEX IF EXISTS idx_tm_subdistricts_coords;

ALTER TABLE tm_cities DROP COLUMN IF EXISTS latitude;
ALTER TABLE tm_cities DROP COLUMN IF EXISTS longitude;

ALTER TABLE tm_subdistricts DROP COLUMN IF EXISTS latitude;
ALTER TABLE tm_subdistricts DROP COLUMN IF EXISTS longitude;
