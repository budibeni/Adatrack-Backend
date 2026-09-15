-- ============================================================================
-- Migration: MASTER 016 — seed tm_modules + tm_menus from docs/FRONTEND.md
-- ============================================================================
-- Idempotent (ON CONFLICT ... DO UPDATE) so re-running never duplicates rows and
-- a taxonomy change in FRONTEND.md is picked up by re-applying the seed.
--
-- Code convention: menu code = <app>.<module_code>.<menu_code>
--   e.g. business.tracking.live_map (matches the PRD §6.1 example)
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 1. Modules (FRONTEND.md §1 Business, §2 Personal)
-- ---------------------------------------------------------------------------
INSERT INTO tm_modules (code, name, app, description, sort_order) VALUES
    ('main',        'Utama',               'business', 'Dashboard, pemantauan, perjalanan', 1),
    ('master-data', 'Master Data',         'business', 'Armada, pengemudi, geofence, grup, rute', 2),
    ('access',      'Akses',               'business', 'Personel, kartu RFID, log akses', 3),
    ('asset',       'Aset & Perawatan',    'business', 'Aset dan penjadwalan perawatan', 4),
    ('safety',      'Keamanan',            'business', 'Skor keamanan dan insiden', 5),
    ('analysis',    'Analisis & Laporan',  'business', 'Laporan dan analitik lanjutan', 6),
    ('industry',    'Modul Spesifik Industri', 'business', 'Rental, transport, logistics, sales, field service, patrol, project site', 7),
    ('admin',       'Administrasi',        'business', 'Pengguna, organisasi, perangkat GPS, integrasi, pengaturan', 8),
    ('tracking',    'Pemantauan',          'personal', 'Peta lokasi aset pribadi', 1),
    ('statistics',  'Statistik',           'personal', 'Ringkasan aktivitas dan metrik penggunaan', 2),
    ('settings',    'Pengaturan',          'personal', 'Bahasa, tema, notifikasi dasar', 3)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    app = EXCLUDED.app,
    description = EXCLUDED.description,
    sort_order = EXCLUDED.sort_order;
-- ---------------------------------------------------------------------------
-- 2. Top-level menus (parents inserted FIRST so the child lookups in step 3
--    resolve against already-committed rows).
-- ---------------------------------------------------------------------------
WITH menu_seed(module_code, code, name, path, sort_order) AS (
    VALUES
        -- Business → Utama (§1.1)
        ('main',        'business.main.home',            'Beranda (Home)',          '/',              1),
        ('main',        'business.main.tracking',        'Pemantauan (Tracking)',   '/tracking',      2),
        ('main',        'business.main.trips',           'Perjalanan (Trips)',      '/trips',         3),
        -- Business → Master Data (§1.2)
        ('master-data', 'business.master-data.vehicles', 'Armada (Vehicles)',       '/vehicles',      1),
        ('master-data', 'business.master-data.drivers',  'Pengemudi (Drivers)',     '/drivers',       2),
        ('master-data', 'business.master-data.geofences','Geofence (Geofences)',    '/geofences',     3),
        ('master-data', 'business.master-data.groups',   'Grup (Groups)',           '/groups',        4),
        ('master-data', 'business.master-data.routes',   'Rute (Routes)',           '/routes',        5),
        -- Business → Akses (§1.3)
        ('access',      'business.access.personel',      'Personel (Personel)',     '/personel',      1),
        ('access',      'business.access.card',          'Kartu (Card)',            '/card',          2),
        ('access',      'business.access.log',           'Log (Log)',               '/log',           3),
        -- Business → Aset & Perawatan (§1.4)
        ('asset',       'business.asset.assets',         'Aset (Assets)',           '/assets',        1),
        ('asset',       'business.asset.maintenance',    'Perawatan (Maintenance)', '/maintenance',   2),
        -- Business → Keamanan (§1.5)
        ('safety',      'business.safety.safety',        'Keamanan (Safety)',       '/safety',        1),
        ('safety',      'business.safety.incidents',     'Insiden (Incidents)',     '/incidents',     2),
        -- Business → Analisis & Laporan (§1.6)
        ('analysis',    'business.analysis.reports',     'Laporan (Reports)',       '/reports',       1),
        ('analysis',    'business.analysis.analytics',   'Analitik (Analytics)',    '/analytics',     2),
        -- Business → Modul Spesifik Industri (§1.7 — parents of industry pages)
        ('industry',    'business.industry.rental',        'Rental',         '/rental',        1),
        ('industry',    'business.industry.transport',     'Transport',      '/transport',     2),
        ('industry',    'business.industry.logistics',     'Logistics',      '/logistics',     3),
        ('industry',    'business.industry.sales',         'Sales',          '/sales',         4),
        ('industry',    'business.industry.field-service', 'Field Service',  '/field-service', 5),
        ('industry',    'business.industry.patrol',        'Patrol',         '/patrol',        6),
        ('industry',    'business.industry.project',       'Project Site',   '/project',       7),
        -- Business → Administrasi (§1.8)
        ('admin',       'business.admin.users',          'Akses Pengguna (Users Access)', '/users',        1),
        ('admin',       'business.admin.organization',   'Organisasi (Organization)',     '/organization', 2),
        ('admin',       'business.admin.gps-devices',    'Perangkat GPS (GPS Devices)',   '/gps-devices',  3),
        ('admin',       'business.admin.integrations',   'Integrasi (Integrations)',      '/integrations', 4),
        ('admin',       'business.admin.settings',       'Pengaturan (Settings)',         '/settings',     5),
        -- Personal (§2)
        ('tracking',    'personal.tracking.map',         'Pemantauan (Tracking)',   '/',              1),
        ('statistics',  'personal.statistics.summary',   'Statistik (Statistics)',  '/statistics',    1),
        ('settings',    'personal.settings.preferences', 'Pengaturan (Settings)',   '/settings',      1)
)
INSERT INTO tm_menus (module_id, code, name, path, sort_order)
SELECT m.id, s.code, s.name, s.path, s.sort_order
FROM menu_seed s
JOIN tm_modules m ON m.code = s.module_code
ON CONFLICT (code) DO UPDATE SET
    module_id = EXCLUDED.module_id,
    name = EXCLUDED.name,
    path = EXCLUDED.path,
    sort_order = EXCLUDED.sort_order;

-- ---------------------------------------------------------------------------
-- 3. Submenu pages (parents already exist from step 2)
-- ---------------------------------------------------------------------------
WITH menu_seed(parent_code, code, name, path, sort_order) AS (
    VALUES
        -- Pemantauan → Live Map / Heatmap / Playback (§1.1)
        ('business.main.tracking', 'business.tracking.live_map', 'Live Map', '/tracking',          1),
        ('business.main.tracking', 'business.tracking.heatmap',  'Heatmap',  '/tracking/heatmap',  2),
        ('business.main.tracking', 'business.tracking.playback', 'Playback', '/tracking/playback', 3),
        -- Rental (§1.7)
        ('business.industry.rental', 'business.industry.rental.customers',    'Pelanggan Rental',   '/rental/customers',    1),
        ('business.industry.rental', 'business.industry.rental.vehicles',     'Armada Rental',      '/rental/vehicles',     2),
        ('business.industry.rental', 'business.industry.rental.reservations', 'Reservasi',          '/rental/reservations', 3),
        ('business.industry.rental', 'business.industry.rental.contracts',    'Kontrak',            '/rental/contracts',    4),
        ('business.industry.rental', 'business.industry.rental.handovers',    'Serah Terima',       '/rental/handovers',    5),
        ('business.industry.rental', 'business.industry.rental.returns',      'Pengembalian',       '/rental/returns',      6),
        ('business.industry.rental', 'business.industry.rental.reports',      'Laporan Rental',     '/rental/reports',      7),
        -- Transport (§1.7)
        ('business.industry.transport', 'business.industry.transport.dashboard',  'Dashboard Transport', '/transport/dashboard',  1),
        ('business.industry.transport', 'business.industry.transport.vehicles',   'Armada Transport',    '/transport/vehicles',   2),
        ('business.industry.transport', 'business.industry.transport.schedules',  'Jadwal',              '/transport/schedules',  3),
        ('business.industry.transport', 'business.industry.transport.departures', 'Keberangkatan',       '/transport/departures', 4),
        ('business.industry.transport', 'business.industry.transport.checker',    'Checker',             '/transport/checker',    5),
        -- Logistics (§1.7)
        ('business.industry.logistics', 'business.industry.logistics.dashboard',  'Dashboard Logistics', '/logistics/dashboard',  1),
        ('business.industry.logistics', 'business.industry.logistics.customers',  'Pelanggan',           '/logistics/customers',  2),
        ('business.industry.logistics', 'business.industry.logistics.orders',     'Order',               '/logistics/orders',     3),
        ('business.industry.logistics', 'business.industry.logistics.shipments',  'Pengiriman',          '/logistics/shipments',  4),
        ('business.industry.logistics', 'business.industry.logistics.manifests',  'Manifest',            '/logistics/manifests',  5),
        ('business.industry.logistics', 'business.industry.logistics.deliveries', 'Delivery',            '/logistics/deliveries', 6),
        ('business.industry.logistics', 'business.industry.logistics.pod',        'Bukti Terima (POD)',  '/logistics/pod',        7),
        ('business.industry.logistics', 'business.industry.logistics.reports',    'Laporan Logistics',   '/logistics/reports',    8),
        -- Sales (§1.7)
        ('business.industry.sales', 'business.industry.sales.dashboard',  'Dashboard Sales',    '/sales/dashboard',  1),
        ('business.industry.sales', 'business.industry.sales.customers',  'Pelanggan',          '/sales/customers',  2),
        ('business.industry.sales', 'business.industry.sales.visits',     'Kunjungan',          '/sales/visits',     3),
        ('business.industry.sales', 'business.industry.sales.prospects',  'Prospek',            '/sales/prospects',  4),
        ('business.industry.sales', 'business.industry.sales.quotes',     'Penawaran',          '/sales/quotes',     5),
        ('business.industry.sales', 'business.industry.sales.orders',     'Order Penjualan',    '/sales/orders',     6),
        ('business.industry.sales', 'business.industry.sales.reports',    'Laporan Sales',      '/sales/reports',    7)
)
INSERT INTO tm_menus (module_id, parent_id, code, name, path, sort_order)
SELECT p.module_id, p.id, s.code, s.name, s.path, s.sort_order
FROM menu_seed s
JOIN tm_menus p ON p.code = s.parent_code
ON CONFLICT (code) DO UPDATE SET
    module_id = EXCLUDED.module_id,
    parent_id = EXCLUDED.parent_id,
    name = EXCLUDED.name,
    path = EXCLUDED.path,
    sort_order = EXCLUDED.sort_order;
-- ---------------------------------------------------------------------------
-- 4. Submenu pages, part 2 — Field Service / Patrol / Project Site (§1.7)
-- ---------------------------------------------------------------------------
WITH menu_seed(parent_code, code, name, path, sort_order) AS (
    VALUES
        ('business.industry.field-service', 'business.industry.field-service.dashboard',   'Dashboard Field Service', '/field-service/dashboard',   1),
        ('business.industry.field-service', 'business.industry.field-service.customers',   'Pelanggan',               '/field-service/customers',   2),
        ('business.industry.field-service', 'business.industry.field-service.work-orders', 'Work Order',              '/field-service/work-orders', 3),
        ('business.industry.field-service', 'business.industry.field-service.assignments', 'Penugasan',               '/field-service/assignments', 4),
        ('business.industry.field-service', 'business.industry.field-service.schedules',   'Jadwal',                  '/field-service/schedules',   5),
        ('business.industry.field-service', 'business.industry.field-service.technicians', 'Teknisi',                 '/field-service/technicians', 6),
        ('business.industry.field-service', 'business.industry.field-service.completions', 'Penyelesaian',            '/field-service/completions', 7),
        ('business.industry.field-service', 'business.industry.field-service.reports',     'Laporan',                 '/field-service/reports',     8),
        ('business.industry.patrol', 'business.industry.patrol.dashboard',   'Dashboard Patrol', '/patrol/dashboard',   1),
        ('business.industry.patrol', 'business.industry.patrol.schedules',   'Jadwal',           '/patrol/schedules',   2),
        ('business.industry.patrol', 'business.industry.patrol.assignments', 'Penugasan',        '/patrol/assignments', 3),
        ('business.industry.patrol', 'business.industry.patrol.checkpoints', 'Checkpoint',       '/patrol/checkpoints', 4),
        ('business.industry.patrol', 'business.industry.patrol.inspections', 'Inspeksi',         '/patrol/inspections', 5),
        ('business.industry.patrol', 'business.industry.patrol.incidents',   'Insiden',          '/patrol/incidents',   6),
        ('business.industry.patrol', 'business.industry.patrol.reports',     'Laporan',          '/patrol/reports',     7),
        ('business.industry.patrol', 'business.industry.patrol.history',     'Riwayat',          '/patrol/history',     8),
        ('business.industry.project', 'business.industry.project.dashboard',   'Dashboard Project', '/project/dashboard',   1),
        ('business.industry.project', 'business.industry.project.projects',    'Proyek',            '/project/projects',    2),
        ('business.industry.project', 'business.industry.project.sites',       'Site',              '/project/sites',       3),
        ('business.industry.project', 'business.industry.project.assignments', 'Penugasan',         '/project/assignments', 4),
        ('business.industry.project', 'business.industry.project.schedules',   'Jadwal',            '/project/schedules',   5),
        ('business.industry.project', 'business.industry.project.activities',  'Aktivitas',         '/project/activities',  6),
        ('business.industry.project', 'business.industry.project.incidents',   'Insiden',           '/project/incidents',   7),
        ('business.industry.project', 'business.industry.project.reports',     'Laporan',           '/project/reports',     8)
)
INSERT INTO tm_menus (module_id, parent_id, code, name, path, sort_order)
SELECT p.module_id, p.id, s.code, s.name, s.path, s.sort_order
FROM menu_seed s
JOIN tm_menus p ON p.code = s.parent_code
ON CONFLICT (code) DO UPDATE SET
    module_id = EXCLUDED.module_id,
    parent_id = EXCLUDED.parent_id,
    name = EXCLUDED.name,
    path = EXCLUDED.path,
    sort_order = EXCLUDED.sort_order;