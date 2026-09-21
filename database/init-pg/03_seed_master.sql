SET search_path TO adatrack_gps_master;

INSERT INTO tm_vehicle_categories (code, name) VALUES 
('PVB', 'Passenger Vehicle'), ('LCV', 'Light Commercial Vehicle'), ('HCV', 'Heavy Commercial Vehicle'),
('TW', 'Two Wheeler'), ('THW', 'Three Wheeler'), ('EV', 'Electric Vehicle'), ('SPV', 'Special Purpose Vehicle')
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_vehicle_types (code, category_code, name) VALUES 
('SEDAN', 'PVB', 'Sedan'), ('SUV', 'PVB', 'Sport Utility Vehicle'), ('MPV', 'PVB', 'Multi Purpose Vehicle'),
('PICKUP', 'LCV', 'Pick-Up Truck'), ('VAN', 'LCV', 'Cargo Van'), ('TRUCK_LIGHT', 'LCV', 'Light Duty Truck'),
('TRUCK_HEAVY', 'HCV', 'Heavy Duty Truck'), ('TRAILER', 'HCV', 'Tractor Trailer'), ('BUS', 'HCV', 'Passenger Bus'),
('MOTORCYCLE', 'TW', 'Motorcycle'), ('TRICYCLE', 'THW', 'Tricycle'), ('EV_CAR', 'EV', 'Electric Car'),
('EV_BIKE', 'EV', 'Electric Bike'), ('AMBULANCE', 'SPV', 'Ambulance'), ('FIRE_TRUCK', 'SPV', 'Fire Engine'),
('EXCAVATOR', 'SPV', 'Excavator')
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_modules (code, name, app, sort_order) VALUES 
('business.utama', 'Utama', 'business', 10),
('business.master_data', 'Master Data', 'business', 20),
('business.akses', 'Manajemen Akses', 'business', 30),
('business.aset', 'Aset & Perawatan', 'business', 40),
('business.keamanan', 'Keamanan & Notifikasi', 'business', 50),
('business.analisis', 'Analisis & Laporan', 'business', 60),
('business.industry', 'Industry Specific', 'business', 70),
('business.administrasi', 'Administrasi', 'business', 80),
('personal.tracking', 'Tracking', 'personal', 100),
('personal.statistics', 'Statistics', 'personal', 110),
('personal.settings', 'Settings', 'personal', 120)
ON CONFLICT (code) DO NOTHING;

DO $$ 
DECLARE
    m_utama INT; m_master INT; m_keamanan INT; p_track INT;
BEGIN
    SELECT id INTO m_utama FROM tm_modules WHERE code = 'business.utama';
    SELECT id INTO m_master FROM tm_modules WHERE code = 'business.master_data';
    SELECT id INTO m_keamanan FROM tm_modules WHERE code = 'business.keamanan';
    SELECT id INTO p_track FROM tm_modules WHERE code = 'personal.tracking';

    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_utama, 'business.utama.dashboard', 'Dashboard', '/dashboard', 1),
    (m_utama, 'business.utama.live_map', 'Live Map', '/live-map', 2) ON CONFLICT (code) DO NOTHING;

    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_master, 'business.master.vehicles', 'Manajemen Kendaraan', '/master/vehicles', 1),
    (m_master, 'business.master.drivers', 'Manajemen Pengemudi', '/master/drivers', 2),
    (m_master, 'business.master.geofences', 'Manajemen Geofence', '/master/geofences', 3),
    (m_master, 'business.master.routes', 'Manajemen Rute', '/master/routes', 4) ON CONFLICT (code) DO NOTHING;

    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_keamanan, 'business.security.alerts', 'Log Peringatan', '/security/alerts', 1),
    (m_keamanan, 'business.security.rules', 'Aturan Keamanan', '/security/rules', 2) ON CONFLICT (code) DO NOTHING;

    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (p_track, 'personal.tracking.map', 'My Vehicles Map', '/personal/map', 1),
    (p_track, 'personal.tracking.history', 'Trip History', '/personal/history', 2) ON CONFLICT (code) DO NOTHING;
END $$;

INSERT INTO tm_countries (code, name) VALUES ('ID', 'Indonesia') ON CONFLICT (code) DO NOTHING;
-- Note: Provinces, Cities, Districts, Subdistricts are imported via Go batch worker.

INSERT INTO tm_companies (code, name, legal_name, tax_id, country_code, business_type)
VALUES ('DEFAULT', 'Adatrack System', 'PT Adatrack Teknologi', '00.000.000.0-000.000', 'ID', 'b2b')
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_roles (code, name, is_system, permissions) VALUES
('SUPER_ADMIN', 'Super Admin', true, '["*"]'::jsonb),
('ADMIN', 'Admin', true, '["users:read", "users:write", "vehicles:read", "vehicles:write"]'::jsonb),
('MANAGER', 'Manager', true, '["vehicles:read", "reports:read"]'::jsonb),
('DRIVER', 'Driver', true, '["vehicles:read"]'::jsonb),
('OPERATOR', 'Operator', true, '["vehicles:read", "alerts:read"]'::jsonb),
('CUSTOMER_SERVICE', 'Customer Service', true, '["users:read", "vehicles:read"]'::jsonb)
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_users (email, password_hash, must_change_password) 
VALUES ('superadmin@adatrack.local', '$2a$12$e/M.q9oFq1hH4KqJv6T.7O2s3b5K5tE7xR8Wq8N/kCqP6D.LqF6Xy', false)
ON CONFLICT (email) DO NOTHING;
