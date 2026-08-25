-- ============================================================================
-- MASTER reference 002 — provinces (BPS Kemendagri, ID only)
-- AUTO-GENERATED oleh backend/database/tools/genregions (jangan edit manual).
-- Sumber: fityannugroho/idn-area-data provinces.csv + geonames ADM2 centroid coords
-- Idempotent: INSERT ... ON DUPLICATE KEY UPDATE.
-- ============================================================================


INSERT INTO provinces (country_id, code, name, latitude, longitude) VALUES
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '11', 'Aceh', 4.847888, 96.480446),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '12', 'Sumatera Utara', 1.749660, 98.851535),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '13', 'Sumatera Barat', -0.897364, 100.521328),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '14', 'Riau', 0.814089, 101.697646),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '15', 'Jambi', -1.567827, 102.638959),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '16', 'Sumatera Selatan', -3.557916, 103.768499),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '17', 'Bengkulu', -3.724180, 102.446443),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '18', 'Lampung', -4.902827, 104.956205),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '19', 'Kepulauan Bangka Belitung', -2.482252, 106.454085),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '21', 'Kepulauan Riau', 1.553044, 104.951704),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '31', 'Daerah Khusus Ibukota Jakarta', -5.803080, 106.522570),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '32', 'Jawa Barat', -7.667300, 108.640370),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '33', 'Jawa Tengah', -7.100690, 109.798115),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '34', 'Daerah Istimewa Yogyakarta', -7.747115, 110.237330),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '35', 'Jawa Timur', -7.513010, 112.545255),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '36', 'Banten', -6.209680, 106.431215),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '51', 'Bali', NULL, NULL),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '52', 'Nusa Tenggara Barat', -8.488340, 118.617095),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '53', 'Nusa Tenggara Timur', -9.338059, 121.541867),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '61', 'Kalimantan Barat', 0.049277, 110.156048),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '62', 'Kalimantan Tengah', -1.769790, 113.731260),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '63', 'Kalimantan Selatan', -3.191383, 115.134213),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '64', 'Kalimantan Timur', -0.261316, 116.811928),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '65', 'Kalimantan Utara', NULL, NULL),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '71', 'Sulawesi Utara', 1.322318, 124.663453),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '72', 'Sulawesi Tengah', -1.216178, 122.016457),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '73', 'Sulawesi Selatan', -4.240820, 120.234320),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '74', 'Sulawesi Tenggara', -4.569537, 122.558900),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '75', 'Gorontalo', 0.664977, 122.893573),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '76', 'Sulawesi Barat', -2.493260, 119.423425),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '81', 'Maluku', -4.698159, 129.152489),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '82', 'Maluku Utara', 0.511811, 127.214620),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '91', 'Papua', -2.027740, 137.418806),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '92', 'Papua Barat', -2.108573, 133.626430),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '93', 'Papua Selatan', NULL, NULL),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '94', 'Papua Tengah', -3.788982, 136.468208),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '95', 'Papua Pegunungan', -3.978462, 138.809942),
  ((SELECT id FROM countries WHERE iso_code = 'ID'), '96', 'Papua Barat Daya', -1.195173, 131.962940)
ON DUPLICATE KEY UPDATE name=VALUES(name), latitude=VALUES(latitude), longitude=VALUES(longitude);
