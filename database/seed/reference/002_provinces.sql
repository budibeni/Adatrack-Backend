-- ============================================================================
-- MASTER reference seed — tm_provinces (38 provinsi Indonesia)
-- ============================================================================
-- Source data: fityannugroho/idn-area-data provinces.csv + geonames ADM2 centroid
-- Generated artifact (ADATRACK wilayah generator output) converted from the
-- MySQL flavour to PostgreSQL:
--   * normalized table names with the tm_ prefix (PRD §6.0)
--   * INSERT ... 
-- Re-running this file never errors (B0 acceptance: "init-pg idempoten").
-- Applied by database/init-pg/02b-seed-reference.sh and scripts/migrate.sh.
-- ============================================================================



INSERT INTO adatrack_gps_master.tm_provinces (id, country_code, name) VALUES
  (11, 'ID', 'Aceh'),
  (12, 'ID', 'Sumatera Utara'),
  (13, 'ID', 'Sumatera Barat'),
  (14, 'ID', 'Riau'),
  (15, 'ID', 'Jambi'),
  (16, 'ID', 'Sumatera Selatan'),
  (17, 'ID', 'Bengkulu'),
  (18, 'ID', 'Lampung'),
  (19, 'ID', 'Kepulauan Bangka Belitung'),
  (21, 'ID', 'Kepulauan Riau'),
  (31, 'ID', 'Daerah Khusus Ibukota Jakarta'),
  (32, 'ID', 'Jawa Barat'),
  (33, 'ID', 'Jawa Tengah'),
  (34, 'ID', 'Daerah Istimewa Yogyakarta'),
  (35, 'ID', 'Jawa Timur'),
  (36, 'ID', 'Banten'),
  (51, 'ID', 'Bali'),
  (52, 'ID', 'Nusa Tenggara Barat'),
  (53, 'ID', 'Nusa Tenggara Timur'),
  (61, 'ID', 'Kalimantan Barat'),
  (62, 'ID', 'Kalimantan Tengah'),
  (63, 'ID', 'Kalimantan Selatan'),
  (64, 'ID', 'Kalimantan Timur'),
  (65, 'ID', 'Kalimantan Utara'),
  (71, 'ID', 'Sulawesi Utara'),
  (72, 'ID', 'Sulawesi Tengah'),
  (73, 'ID', 'Sulawesi Selatan'),
  (74, 'ID', 'Sulawesi Tenggara'),
  (75, 'ID', 'Gorontalo'),
  (76, 'ID', 'Sulawesi Barat'),
  (81, 'ID', 'Maluku'),
  (82, 'ID', 'Maluku Utara'),
  (91, 'ID', 'Papua'),
  (92, 'ID', 'Papua Barat'),
  (93, 'ID', 'Papua Selatan'),
  (94, 'ID', 'Papua Tengah'),
  (95, 'ID', 'Papua Pegunungan'),
  (96, 'ID', 'Papua Barat Daya');