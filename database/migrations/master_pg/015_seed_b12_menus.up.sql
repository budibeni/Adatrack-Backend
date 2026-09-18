SET search_path TO adatrack_gps_master;

-- Modules Business
INSERT INTO tm_modules (code, name, app, sort_order) VALUES
('main', 'Utama', 'business', 1),
('master', 'Master Data', 'business', 2),
('access', 'Akses', 'business', 3),
('asset', 'Aset & Perawatan', 'business', 4),
('safety', 'Keamanan', 'business', 5),
('analysis', 'Analisis & Laporan', 'business', 6),
('industry', 'Industri Spesifik', 'business', 7),
('admin', 'Administrasi', 'business', 8)
ON CONFLICT (code) DO NOTHING;

-- Modules Personal
INSERT INTO tm_modules (code, name, app, sort_order) VALUES
('personal_main', 'Utama', 'personal', 1)
ON CONFLICT (code) DO NOTHING;

-- Menus Business
DO $$ 
DECLARE 
    v_main_id INT;
    v_master_id INT;
    v_access_id INT;
    v_asset_id INT;
    v_safety_id INT;
    v_analysis_id INT;
    v_industry_id INT;
    v_admin_id INT;
    v_personal_main_id INT;
BEGIN
    SELECT id INTO v_main_id FROM tm_modules WHERE code = 'main';
    SELECT id INTO v_master_id FROM tm_modules WHERE code = 'master';
    SELECT id INTO v_access_id FROM tm_modules WHERE code = 'access';
    SELECT id INTO v_asset_id FROM tm_modules WHERE code = 'asset';
    SELECT id INTO v_safety_id FROM tm_modules WHERE code = 'safety';
    SELECT id INTO v_analysis_id FROM tm_modules WHERE code = 'analysis';
    SELECT id INTO v_industry_id FROM tm_modules WHERE code = 'industry';
    SELECT id INTO v_admin_id FROM tm_modules WHERE code = 'admin';
    SELECT id INTO v_personal_main_id FROM tm_modules WHERE code = 'personal_main';

    -- 1.1 Utama
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_main_id, 'main.home', 'Beranda', '/', 1),
    (v_main_id, 'main.tracking', 'Pemantauan', '/tracking', 2),
    (v_main_id, 'main.trips', 'Perjalanan', '/trips', 3)
    ON CONFLICT (code) DO NOTHING;

    -- 1.2 Master Data
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_master_id, 'master.vehicles', 'Armada', '/vehicles', 1),
    (v_master_id, 'master.drivers', 'Pengemudi', '/drivers', 2),
    (v_master_id, 'master.geofences', 'Geofence', '/geofences', 3),
    (v_master_id, 'master.groups', 'Grup', '/groups', 4),
    (v_master_id, 'master.routes', 'Rute', '/routes', 5)
    ON CONFLICT (code) DO NOTHING;

    -- 1.3 Akses
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_access_id, 'access.personel', 'Personel', '/personel', 1),
    (v_access_id, 'access.card', 'Kartu', '/card', 2),
    (v_access_id, 'access.log', 'Log', '/log', 3)
    ON CONFLICT (code) DO NOTHING;

    -- 1.4 Aset
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_asset_id, 'asset.assets', 'Aset', '/assets', 1),
    (v_asset_id, 'asset.maintenance', 'Perawatan', '/maintenance', 2)
    ON CONFLICT (code) DO NOTHING;

    -- 1.5 Keamanan
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_safety_id, 'safety.safety', 'Keamanan', '/safety', 1),
    (v_safety_id, 'safety.incidents', 'Insiden', '/incidents', 2)
    ON CONFLICT (code) DO NOTHING;

    -- 1.6 Analisis
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_analysis_id, 'analysis.reports', 'Laporan', '/reports', 1),
    (v_analysis_id, 'analysis.analytics', 'Analitik', '/analytics', 2)
    ON CONFLICT (code) DO NOTHING;

    -- 1.7 Industry (skip for now or add one)
    -- ...

    -- 1.8 Admin
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_admin_id, 'admin.users', 'Akses Pengguna', '/users', 1),
    (v_admin_id, 'admin.organization', 'Organisasi', '/organization', 2),
    (v_admin_id, 'admin.gps_devices', 'Perangkat GPS', '/gps-devices', 3),
    (v_admin_id, 'admin.integrations', 'Integrasi', '/integrations', 4),
    (v_admin_id, 'admin.settings', 'Pengaturan', '/settings', 5)
    ON CONFLICT (code) DO NOTHING;

    -- 2 Personal
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_personal_main_id, 'personal.tracking', 'Pemantauan', '/', 1),
    (v_personal_main_id, 'personal.statistics', 'Statistik', '/statistics', 2),
    (v_personal_main_id, 'personal.settings', 'Pengaturan', '/settings', 3)
    ON CONFLICT (code) DO NOTHING;

END $$;
