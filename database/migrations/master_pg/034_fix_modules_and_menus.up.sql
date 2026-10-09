-- Fix duplicate modules and standardize to frontend sidebar map

-- 1. Hapus semua relasi menu lama
DELETE FROM tm_menus;
DELETE FROM tm_modules;

-- 2. Reset Sequence
ALTER SEQUENCE tm_modules_id_seq RESTART WITH 1;
ALTER SEQUENCE tm_menus_id_seq RESTART WITH 1;

-- 3. Insert Modules (Sidebar Groups)
INSERT INTO tm_modules (code, name, app, sort_order) VALUES
('main', 'Utama', 'business', 1),
('master', 'Master Data', 'business', 2),
('access', 'Access Cards', 'business', 3),
('asset', 'Assets', 'business', 4),
('safety', 'Safety', 'business', 5),
('analysis', 'Analysis & Reports', 'business', 6),
('rental', 'Rental', 'business', 7),
('transport', 'Transport', 'business', 8),
('logistics', 'Logistics', 'business', 9),
('sales', 'Sales', 'business', 10),
('field_service', 'Field Service', 'business', 11),
('patrol', 'Patrol', 'business', 12),
('project_site', 'Project / Site', 'business', 13),
('admin', 'Administration', 'business', 14),
('personal.tracking', 'Personal Tracking', 'personal', 100),
('personal.statistics', 'Personal Statistics', 'personal', 101),
('personal.settings', 'Personal Settings', 'personal', 102);

-- 4. Insert Menus (Items in Sidebar)
DO $$
DECLARE
    v_main_id INT;
    v_master_id INT;
    v_access_id INT;
    v_asset_id INT;
    v_admin_id INT;
BEGIN
    SELECT id INTO v_main_id FROM tm_modules WHERE code = 'main';
    SELECT id INTO v_master_id FROM tm_modules WHERE code = 'master';
    SELECT id INTO v_access_id FROM tm_modules WHERE code = 'access';
    SELECT id INTO v_asset_id FROM tm_modules WHERE code = 'asset';
    SELECT id INTO v_admin_id FROM tm_modules WHERE code = 'admin';

    -- Menus for MAIN
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_main_id, 'main.home', 'Home', '/', 1),
    (v_main_id, 'main.tracking', 'Tracking', '/tracking', 2);

    -- Menus for MASTER
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_master_id, 'master.vehicles', 'Vehicles', '/vehicles', 1),
    (v_master_id, 'master.drivers', 'Drivers', '/drivers', 2),
    (v_master_id, 'master.geofences', 'Geofences', '/geofences', 3),
    (v_master_id, 'master.routes', 'Routes', '/routes', 4),
    (v_master_id, 'master.groups', 'Groups', '/groups', 5);

    -- Menus for ACCESS
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_access_id, 'access.personel', 'Personel', '/personel', 1),
    (v_access_id, 'access.card', 'Access Card', '/card', 2),
    (v_access_id, 'access.log', 'Access Log', '/log', 3);

    -- Menus for ASSET
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_asset_id, 'asset.assets', 'Assets', '/assets', 1),
    (v_asset_id, 'asset.maintenance', 'Maintenance', '/maintenance', 2);
    
    -- Menus for ADMIN
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_admin_id, 'admin.users', 'Users & Access', '/users', 1),
    (v_admin_id, 'admin.organization', 'Organization', '/organization', 2),
    (v_admin_id, 'admin.gps_devices', 'GPS & Devices', '/gps-devices', 3),
    (v_admin_id, 'admin.integrations', 'Integrations', '/integrations', 4),
    (v_admin_id, 'admin.settings', 'Settings', '/settings', 5);
END $$;
