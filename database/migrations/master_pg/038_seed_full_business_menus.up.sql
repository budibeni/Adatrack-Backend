-- ==============================================================================
-- Migration 038: Seed Full Business Modules & Menus in Master DB
-- ==============================================================================

-- 1. Ensure all Modules exist in adatrack_gps_master.tm_modules
INSERT INTO adatrack_gps_master.tm_modules (id, code, name, app, sort_order, enabled) VALUES
  (1, 'main', 'Utama', 'business', 1, true),
  (2, 'master', 'Master Data', 'business', 2, true),
  (3, 'access', 'Akses Kontrol', 'business', 3, true),
  (4, 'asset', 'Manajemen Aset', 'business', 4, true),
  (5, 'safety', 'Keselamatan', 'business', 5, true),
  (6, 'analysis', 'Analisis & Laporan', 'business', 6, true),
  (7, 'rental', 'Rental Kendaraan', 'business', 7, true),
  (8, 'transport', 'Transportasi', 'business', 8, true),
  (9, 'logistics', 'Logistik', 'business', 9, true),
  (10, 'sales', 'Sales & CRM', 'business', 10, true),
  (11, 'field_service', 'Field Service', 'business', 11, true),
  (12, 'patrol', 'Patroli', 'business', 12, true),
  (13, 'project_site', 'Proyek & Site', 'business', 13, true),
  (14, 'admin', 'Administrasi', 'business', 14, true)
ON CONFLICT (id) DO UPDATE SET 
  name = EXCLUDED.name,
  enabled = true;

SELECT setval('adatrack_gps_master.tm_modules_id_seq', (SELECT MAX(id) FROM adatrack_gps_master.tm_modules));

-- 2. Seed all 75 navigation menus into adatrack_gps_master.tm_menus
INSERT INTO adatrack_gps_master.tm_menus (module_id, code, name, path, sort_order, enabled) VALUES
  -- Module 1: Utama
  (1, 'main.home', 'Home', '/', 1, true),
  (1, 'main.tracking', 'Tracking', '/tracking', 2, true),

  -- Module 2: Master Data
  (2, 'master.vehicles', 'Kendaraan', '/vehicles', 1, true),
  (2, 'master.drivers', 'Pengemudi', '/drivers', 2, true),
  (2, 'master.geofences', 'Geofence', '/geofences', 3, true),
  (2, 'master.routes', 'Rute', '/routes', 4, true),
  (2, 'master.groups', 'Grup Kendaraan', '/groups', 5, true),

  -- Module 3: Akses Kontrol
  (3, 'access.personel', 'Personel', '/personel', 1, true),
  (3, 'access.card', 'Kartu Akses', '/card', 2, true),
  (3, 'access.log', 'Log Akses', '/log', 3, true),

  -- Module 4: Aset & Pemeliharaan
  (4, 'asset.assets', 'Aset', '/assets', 1, true),
  (4, 'asset.maintenance', 'Pemeliharaan', '/maintenance', 2, true),

  -- Module 5: Keselamatan
  (5, 'safety.safety', 'Keselamatan', '/safety', 1, true),
  (5, 'safety.incidents', 'Insiden', '/incidents', 2, true),

  -- Module 6: Analisis & Laporan
  (6, 'analysis.reports', 'Laporan', '/reports', 1, true),
  (6, 'analysis.analytics', 'Analitik', '/analytics', 2, true),

  -- Module 7: Rental Kendaraan
  (7, 'rental.customers', 'Pelanggan Rental', '/rental/customers', 1, true),
  (7, 'rental.pricing_category', 'Kategori Tarif', '/rental/pricing-category', 2, true),
  (7, 'rental.vehicles', 'Kendaraan Rental', '/rental/vehicles', 3, true),
  (7, 'rental.reservations', 'Reservasi', '/rental/reservations', 4, true),
  (7, 'rental.contracts', 'Kontrak Rental', '/rental/contracts', 5, true),
  (7, 'rental.handovers', 'Serah Terima', '/rental/handovers', 6, true),
  (7, 'rental.returns', 'Pengembalian', '/rental/returns', 7, true),
  (7, 'rental.reports', 'Laporan Rental', '/rental/reports', 8, true),

  -- Module 8: Transportasi
  (8, 'transport.dashboard', 'Dashboard Transport', '/transport/dashboard', 1, true),
  (8, 'transport.vehicles', 'Armada Transport', '/transport/vehicles', 2, true),
  (8, 'transport.schedules', 'Jadwal Operasional', '/transport/schedules', 3, true),
  (8, 'transport.departures', 'Keberangkatan', '/transport/departures', 4, true),
  (8, 'transport.checker', 'Checker Penumpang', '/transport/checker', 5, true),

  -- Module 9: Logistik
  (9, 'logistics.dashboard', 'Dashboard Logistik', '/logistics/dashboard', 1, true),
  (9, 'logistics.customers', 'Pelanggan Logistik', '/logistics/customers', 2, true),
  (9, 'logistics.orders', 'Order Pengiriman', '/logistics/orders', 3, true),
  (9, 'logistics.shipments', 'Pengiriman', '/logistics/shipments', 4, true),
  (9, 'logistics.manifests', 'Manifest', '/logistics/manifests', 5, true),
  (9, 'logistics.deliveries', 'Pengantaran', '/logistics/deliveries', 6, true),
  (9, 'logistics.pod', 'Bukti Pengiriman (POD)', '/logistics/pod', 7, true),
  (9, 'logistics.reports', 'Laporan Logistik', '/logistics/reports', 8, true),

  -- Module 10: Sales & CRM
  (10, 'sales.dashboard', 'Dashboard Sales', '/sales/dashboard', 1, true),
  (10, 'sales.customers', 'Pelanggan Sales', '/sales/customers', 2, true),
  (10, 'sales.visits', 'Kunjungan Sales', '/sales/visits', 3, true),
  (10, 'sales.prospects', 'Prospek', '/sales/prospects', 4, true),
  (10, 'sales.quotes', 'Penawaran (Quotes)', '/sales/quotes', 5, true),
  (10, 'sales.orders', 'Sales Order', '/sales/orders', 6, true),
  (10, 'sales.reports', 'Laporan Sales', '/sales/reports', 7, true),

  -- Module 11: Field Service
  (11, 'field_service.dashboard', 'Dashboard Field Service', '/field-service/dashboard', 1, true),
  (11, 'field_service.customers', 'Pelanggan Layanan', '/field-service/customers', 2, true),
  (11, 'field_service.work_orders', 'Work Order', '/field-service/work-orders', 3, true),
  (11, 'field_service.assignments', 'Penugasan Teknisi', '/field-service/assignments', 4, true),
  (11, 'field_service.schedules', 'Jadwal Layanan', '/field-service/schedules', 5, true),
  (11, 'field_service.technicians', 'Data Teknisi', '/field-service/technicians', 6, true),
  (11, 'field_service.completions', 'Penyelesaian Kerja', '/field-service/completions', 7, true),
  (11, 'field_service.reports', 'Laporan Layanan', '/field-service/reports', 8, true),

  -- Module 12: Patroli
  (12, 'patrol.dashboard', 'Dashboard Patroli', '/patrol/dashboard', 1, true),
  (12, 'patrol.schedules', 'Jadwal Patroli', '/patrol/schedules', 2, true),
  (12, 'patrol.assignments', 'Penugasan Patroli', '/patrol/assignments', 3, true),
  (12, 'patrol.checkpoints', 'Titik Checkpoint', '/patrol/checkpoints', 4, true),
  (12, 'patrol.inspections', 'Inspeksi', '/patrol/inspections', 5, true),
  (12, 'patrol.incidents', 'Insiden Patroli', '/patrol/incidents', 6, true),
  (12, 'patrol.reports', 'Laporan Patroli', '/patrol/reports', 7, true),
  (12, 'patrol.history', 'Riwayat Patroli', '/patrol/history', 8, true),

  -- Module 13: Proyek & Site
  (13, 'project_site.dashboard', 'Dashboard Proyek', '/project/dashboard', 1, true),
  (13, 'project_site.projects', 'Data Proyek', '/project/projects', 2, true),
  (13, 'project_site.sites', 'Lokasi / Site', '/project/sites', 3, true),
  (13, 'project_site.assignments', 'Penugasan Proyek', '/project/assignments', 4, true),
  (13, 'project_site.schedules', 'Jadwal Proyek', '/project/schedules', 5, true),
  (13, 'project_site.activities', 'Aktivitas Proyek', '/project/activities', 6, true),
  (13, 'project_site.incidents', 'Insiden Proyek', '/project/incidents', 7, true),
  (13, 'project_site.reports', 'Laporan Proyek', '/project/reports', 8, true),

  -- Module 14: Administrasi
  (14, 'admin.users', 'Pengguna & Akses', '/users', 1, true),
  (14, 'admin.organization', 'Organisasi', '/organization', 2, true),
  (14, 'admin.gps_devices', 'GPS & Perangkat', '/gps-devices', 3, true),
  (14, 'admin.integrations', 'Integrasi', '/integrations', 4, true),
  (14, 'admin.settings', 'Pengaturan', '/settings', 5, true)
ON CONFLICT (code) DO UPDATE SET
  module_id = EXCLUDED.module_id,
  name = EXCLUDED.name,
  path = EXCLUDED.path,
  sort_order = EXCLUDED.sort_order,
  enabled = true;
