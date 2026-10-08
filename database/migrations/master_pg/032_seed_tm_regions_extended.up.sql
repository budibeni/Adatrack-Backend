SET search_path TO adatrack_gps_master, public;

-- Insert additional major provinces
INSERT INTO tm_regions (id, name, level, parent_id, geom) VALUES
-- BANTEN (6)
(6, 'Banten', 'province', NULL, ST_Multi(ST_MakeEnvelope(105.09, -7.01, 106.77, -5.88, 4326))),

-- SUMATERA (7-11)
(7, 'Sumatera Utara', 'province', NULL, ST_Multi(ST_MakeEnvelope(97.05, 0.54, 100.41, 4.30, 4326))),
(8, 'Sumatera Barat', 'province', NULL, ST_Multi(ST_MakeEnvelope(98.59, -3.00, 101.88, 0.89, 4326))),
(9, 'Riau', 'province', NULL, ST_Multi(ST_MakeEnvelope(100.00, -1.09, 103.81, 2.95, 4326))),
(10, 'Sumatera Selatan', 'province', NULL, ST_Multi(ST_MakeEnvelope(102.05, -4.96, 106.22, -1.63, 4326))),
(11, 'Lampung', 'province', NULL, ST_Multi(ST_MakeEnvelope(103.58, -5.93, 105.90, -3.73, 4326))),

-- KALIMANTAN (12-15)
(12, 'Kalimantan Barat', 'province', NULL, ST_Multi(ST_MakeEnvelope(108.59, -3.09, 114.20, 2.10, 4326))),
(13, 'Kalimantan Timur', 'province', NULL, ST_Multi(ST_MakeEnvelope(113.84, -2.48, 119.04, 2.58, 4326))),
(14, 'Kalimantan Selatan', 'province', NULL, ST_Multi(ST_MakeEnvelope(114.34, -4.33, 116.59, -1.30, 4326))),
(15, 'Kalimantan Tengah', 'province', NULL, ST_Multi(ST_MakeEnvelope(110.73, -3.55, 115.85, -0.63, 4326))),

-- SULAWESI (16-17)
(16, 'Sulawesi Selatan', 'province', NULL, ST_Multi(ST_MakeEnvelope(118.73, -7.50, 121.57, -1.90, 4326))),
(17, 'Sulawesi Utara', 'province', NULL, ST_Multi(ST_MakeEnvelope(123.00, 0.28, 127.13, 5.07, 4326)))
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, geom = EXCLUDED.geom;

-- Update sequence since we hardcoded IDs
SELECT setval('tm_regions_id_seq', (SELECT MAX(id) FROM tm_regions));
