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
DO $$
DECLARE
    v_safety_id INT;
    v_analysis_id INT;
    v_rental_id INT;
    v_transport_id INT;
    v_logistics_id INT;
    v_sales_id INT;
    v_field_service_id INT;
    v_patrol_id INT;
    v_project_site_id INT;
BEGIN
    SELECT id INTO v_safety_id FROM tm_modules WHERE code = 'safety';
    SELECT id INTO v_analysis_id FROM tm_modules WHERE code = 'analysis';
    SELECT id INTO v_rental_id FROM tm_modules WHERE code = 'rental';
    SELECT id INTO v_transport_id FROM tm_modules WHERE code = 'transport';
    SELECT id INTO v_logistics_id FROM tm_modules WHERE code = 'logistics';
    SELECT id INTO v_sales_id FROM tm_modules WHERE code = 'sales';
    SELECT id INTO v_field_service_id FROM tm_modules WHERE code = 'field_service';
    SELECT id INTO v_patrol_id FROM tm_modules WHERE code = 'patrol';
    SELECT id INTO v_project_site_id FROM tm_modules WHERE code = 'project_site';

    -- Menus for SAFETY
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_safety_id, 'safety.safety', 'Safety', '/safety', 1),
    (v_safety_id, 'safety.incidents', 'Incidents', '/incidents', 2);

    -- Menus for ANALYSIS
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_analysis_id, 'analysis.reports', 'Reports', '/reports', 1),
    (v_analysis_id, 'analysis.analytics', 'Analytics', '/analytics', 2);

    -- Menus for RENTAL
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_rental_id, 'rental.customers', 'Customers', '/rental/customers', 1),
    (v_rental_id, 'rental.pricing_category', 'Pricing Category', '/rental/pricing-category', 2),
    (v_rental_id, 'rental.vehicles', 'Vehicles', '/rental/vehicles', 3),
    (v_rental_id, 'rental.reservations', 'Reservations', '/rental/reservations', 4),
    (v_rental_id, 'rental.contracts', 'Contracts', '/rental/contracts', 5),
    (v_rental_id, 'rental.handovers', 'Handovers', '/rental/handovers', 6),
    (v_rental_id, 'rental.returns', 'Returns', '/rental/returns', 7),
    (v_rental_id, 'rental.reports', 'Reports', '/rental/reports', 8);

    -- Menus for TRANSPORT
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_transport_id, 'transport.dashboard', 'Dashboard', '/transport/dashboard', 1),
    (v_transport_id, 'transport.vehicles', 'Vehicles', '/transport/vehicles', 2),
    (v_transport_id, 'transport.schedules', 'Schedules', '/transport/schedules', 3),
    (v_transport_id, 'transport.departures', 'Departures', '/transport/departures', 4),
    (v_transport_id, 'transport.checker', 'Checker', '/transport/checker', 5);

    -- Menus for LOGISTICS
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_logistics_id, 'logistics.dashboard', 'Dashboard', '/logistics/dashboard', 1),
    (v_logistics_id, 'logistics.customers', 'Customers', '/logistics/customers', 2),
    (v_logistics_id, 'logistics.orders', 'Orders', '/logistics/orders', 3),
    (v_logistics_id, 'logistics.shipments', 'Shipments', '/logistics/shipments', 4),
    (v_logistics_id, 'logistics.manifests', 'Manifests', '/logistics/manifests', 5),
    (v_logistics_id, 'logistics.deliveries', 'Deliveries', '/logistics/deliveries', 6),
    (v_logistics_id, 'logistics.pod', 'Proof of Delivery', '/logistics/pod', 7),
    (v_logistics_id, 'logistics.reports', 'Reports', '/logistics/reports', 8);

    -- Menus for SALES
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_sales_id, 'sales.dashboard', 'Dashboard', '/sales/dashboard', 1),
    (v_sales_id, 'sales.customers', 'Customers', '/sales/customers', 2),
    (v_sales_id, 'sales.visits', 'Visits', '/sales/visits', 3),
    (v_sales_id, 'sales.prospects', 'Prospects', '/sales/prospects', 4),
    (v_sales_id, 'sales.quotes', 'Quotes', '/sales/quotes', 5),
    (v_sales_id, 'sales.orders', 'Orders', '/sales/orders', 6),
    (v_sales_id, 'sales.reports', 'Reports', '/sales/reports', 7);

    -- Menus for FIELD SERVICE
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_field_service_id, 'field_service.dashboard', 'Dashboard', '/field-service/dashboard', 1),
    (v_field_service_id, 'field_service.customers', 'Customers', '/field-service/customers', 2),
    (v_field_service_id, 'field_service.work_orders', 'Work Orders', '/field-service/work-orders', 3),
    (v_field_service_id, 'field_service.assignments', 'Assignments', '/field-service/assignments', 4),
    (v_field_service_id, 'field_service.schedules', 'Schedules', '/field-service/schedules', 5),
    (v_field_service_id, 'field_service.technicians', 'Technicians', '/field-service/technicians', 6),
    (v_field_service_id, 'field_service.completions', 'Completions', '/field-service/completions', 7),
    (v_field_service_id, 'field_service.reports', 'Reports', '/field-service/reports', 8);

    -- Menus for PATROL
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_patrol_id, 'patrol.dashboard', 'Dashboard', '/patrol/dashboard', 1),
    (v_patrol_id, 'patrol.schedules', 'Schedules', '/patrol/schedules', 2),
    (v_patrol_id, 'patrol.assignments', 'Assignments', '/patrol/assignments', 3),
    (v_patrol_id, 'patrol.checkpoints', 'Checkpoints', '/patrol/checkpoints', 4),
    (v_patrol_id, 'patrol.inspections', 'Inspections', '/patrol/inspections', 5),
    (v_patrol_id, 'patrol.incidents', 'Incidents', '/patrol/incidents', 6),
    (v_patrol_id, 'patrol.reports', 'Reports', '/patrol/reports', 7),
    (v_patrol_id, 'patrol.history', 'History', '/patrol/history', 8);

    -- Menus for PROJECT SITE
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES
    (v_project_site_id, 'project.dashboard', 'Dashboard', '/project/dashboard', 1),
    (v_project_site_id, 'project.projects', 'Projects', '/project/projects', 2),
    (v_project_site_id, 'project.sites', 'Sites', '/project/sites', 3),
    (v_project_site_id, 'project.assignments', 'Assignments', '/project/assignments', 4),
    (v_project_site_id, 'project.schedules', 'Schedules', '/project/schedules', 5),
    (v_project_site_id, 'project.activities', 'Activities', '/project/activities', 6),
    (v_project_site_id, 'project.incidents', 'Incidents', '/project/incidents', 7),
    (v_project_site_id, 'project.reports', 'Reports', '/project/reports', 8);
END $$;
