SET search_path TO adatrack_gps_master;

ALTER TABLE tm_cities ADD COLUMN IF NOT EXISTS latitude NUMERIC(10, 7);
ALTER TABLE tm_cities ADD COLUMN IF NOT EXISTS longitude NUMERIC(10, 7);

ALTER TABLE tm_subdistricts ADD COLUMN IF NOT EXISTS latitude NUMERIC(10, 7);
ALTER TABLE tm_subdistricts ADD COLUMN IF NOT EXISTS longitude NUMERIC(10, 7);

CREATE INDEX IF NOT EXISTS idx_tm_cities_coords ON tm_cities (latitude, longitude);
CREATE INDEX IF NOT EXISTS idx_tm_subdistricts_coords ON tm_subdistricts (latitude, longitude);

-- Seed major cities coordinates in Indonesia
UPDATE tm_cities SET latitude = -6.2088, longitude = 106.8456 WHERE name ILIKE '%Jakarta%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -7.2575, longitude = 112.7521 WHERE name ILIKE '%Surabaya%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.9175, longitude = 107.6191 WHERE name ILIKE '%Bandung%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = 3.5952, longitude = 98.6722 WHERE name ILIKE '%Medan%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.9667, longitude = 110.4167 WHERE name ILIKE '%Semarang%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -5.1477, longitude = 119.4327 WHERE name ILIKE '%Makassar%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -2.9761, longitude = 104.7754 WHERE name ILIKE '%Palembang%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -8.6705, longitude = 115.2126 WHERE name ILIKE '%Denpasar%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -1.2379, longitude = 116.8289 WHERE name ILIKE '%Balikpapan%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -7.7956, longitude = 110.3695 WHERE name ILIKE '%Yogyakarta%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.5971, longitude = 106.8060 WHERE name ILIKE '%Bogor%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.2383, longitude = 106.9756 WHERE name ILIKE '%Bekasi%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.1783, longitude = 106.6319 WHERE name ILIKE '%Tangerang%' AND latitude IS NULL;
UPDATE tm_cities SET latitude = -6.4025, longitude = 106.7942 WHERE name ILIKE '%Depok%' AND latitude IS NULL;
