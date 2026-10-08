SET search_path TO adatrack_gps_master, public;

-- Delete old seed if any
DELETE FROM tm_regions WHERE name IN ('DKI Jakarta', 'Jawa Barat', 'Jawa Tengah', 'Jawa Timur', 'Bali');

-- Insert Provinces (Level: province)
INSERT INTO tm_regions (id, name, level, parent_id, geom) VALUES
(1, 'DKI Jakarta', 'province', NULL, ST_Multi(ST_MakeEnvelope(106.68, -6.37, 106.98, -6.08, 4326))),
(2, 'Jawa Barat', 'province', NULL, ST_Multi(ST_MakeEnvelope(106.32, -7.82, 108.83, -5.91, 4326))),
(3, 'Jawa Tengah', 'province', NULL, ST_Multi(ST_MakeEnvelope(108.55, -8.21, 111.69, -6.37, 4326))),
(4, 'Jawa Timur', 'province', NULL, ST_Multi(ST_MakeEnvelope(110.89, -8.78, 114.62, -6.74, 4326))),
(5, 'Bali', 'province', NULL, ST_Multi(ST_MakeEnvelope(114.43, -8.84, 115.71, -8.06, 4326)))
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, geom = EXCLUDED.geom;

-- Insert Major Cities (Level: city)
INSERT INTO tm_regions (id, name, level, parent_id, geom) VALUES
(101, 'Jakarta Pusat', 'city', 1, ST_Multi(ST_MakeEnvelope(106.79, -6.21, 106.88, -6.14, 4326))),
(102, 'Jakarta Selatan', 'city', 1, ST_Multi(ST_MakeEnvelope(106.73, -6.37, 106.87, -6.20, 4326))),
(103, 'Bandung', 'city', 2, ST_Multi(ST_MakeEnvelope(107.54, -6.98, 107.74, -6.83, 4326))),
(104, 'Semarang', 'city', 3, ST_Multi(ST_MakeEnvelope(110.28, -7.11, 110.50, -6.93, 4326))),
(105, 'Surabaya', 'city', 4, ST_Multi(ST_MakeEnvelope(112.57, -7.34, 112.80, -7.19, 4326))),
(106, 'Denpasar', 'city', 5, ST_Multi(ST_MakeEnvelope(115.15, -8.74, 115.27, -8.59, 4326)))
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, parent_id = EXCLUDED.parent_id, geom = EXCLUDED.geom;

-- Update sequence since we hardcoded IDs
SELECT setval('tm_regions_id_seq', (SELECT MAX(id) FROM tm_regions));
