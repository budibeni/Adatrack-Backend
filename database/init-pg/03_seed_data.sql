-- ====================================================================================
-- SEED DATA ENTERPRISE (COMPREHENSIVE)
-- Menyediakan seluruh referensi dasar (Wilayah, Kategori, Modul, Menu) sesuai PRD.
-- ====================================================================================

SET search_path TO adatrack_gps_master;

-- 1. SEED KATEGORI DAN TIPE KENDARAAN (PRD §6.1)
INSERT INTO tm_vehicle_categories (code, name) VALUES 
('PVB', 'Passenger Vehicle'),
('LCV', 'Light Commercial Vehicle'),
('HCV', 'Heavy Commercial Vehicle'),
('TW', 'Two Wheeler'),
('THW', 'Three Wheeler'),
('EV', 'Electric Vehicle'),
('SPV', 'Special Purpose Vehicle')
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_vehicle_types (code, category_code, name) VALUES 
('SEDAN', 'PVB', 'Sedan'),
('SUV', 'PVB', 'Sport Utility Vehicle'),
('MPV', 'PVB', 'Multi Purpose Vehicle'),
('PICKUP', 'LCV', 'Pick-Up Truck'),
('VAN', 'LCV', 'Cargo Van'),
('TRUCK_LIGHT', 'LCV', 'Light Duty Truck (Engkel)'),
('TRUCK_HEAVY', 'HCV', 'Heavy Duty Truck (Tronton)'),
('TRAILER', 'HCV', 'Tractor Trailer'),
('BUS', 'HCV', 'Passenger Bus'),
('MOTORCYCLE', 'TW', 'Motorcycle / Scooter'),
('TRICYCLE', 'THW', 'Tricycle / Bajaj'),
('EV_CAR', 'EV', 'Electric Car'),
('EV_BIKE', 'EV', 'Electric Bike'),
('AMBULANCE', 'SPV', 'Ambulance'),
('FIRE_TRUCK', 'SPV', 'Fire Engine'),
('EXCAVATOR', 'SPV', 'Excavator / Heavy Equipment')
ON CONFLICT (code) DO NOTHING;


-- 2. SEED MODUL APLIKASI FRONTEND (PRD §6.1 & docs/FRONTEND.md)
INSERT INTO tm_modules (code, name, app, sort_order) VALUES 
-- Business App Modules
('business.utama', 'Utama', 'business', 10),
('business.master_data', 'Master Data', 'business', 20),
('business.akses', 'Manajemen Akses', 'business', 30),
('business.aset', 'Aset & Perawatan', 'business', 40),
('business.keamanan', 'Keamanan & Notifikasi', 'business', 50),
('business.analisis', 'Analisis & Laporan', 'business', 60),
('business.industry', 'Industry Specific', 'business', 70),
('business.administrasi', 'Administrasi', 'business', 80),
-- Personal App Modules
('personal.tracking', 'Tracking', 'personal', 100),
('personal.statistics', 'Statistics', 'personal', 110),
('personal.settings', 'Settings', 'personal', 120)
ON CONFLICT (code) DO NOTHING;


-- 3. SEED MENU FRONTEND (B2B & B2C)
-- Ambil ID Modul untuk relasi
DO $$ 
DECLARE
    m_utama INT; m_master INT; m_akses INT; m_aset INT; m_keamanan INT; 
    m_analisis INT; m_industry INT; m_admin INT;
    p_track INT; p_stat INT; p_set INT;
BEGIN
    SELECT id INTO m_utama FROM tm_modules WHERE code = 'business.utama';
    SELECT id INTO m_master FROM tm_modules WHERE code = 'business.master_data';
    SELECT id INTO m_akses FROM tm_modules WHERE code = 'business.akses';
    SELECT id INTO m_aset FROM tm_modules WHERE code = 'business.aset';
    SELECT id INTO m_keamanan FROM tm_modules WHERE code = 'business.keamanan';
    SELECT id INTO m_analisis FROM tm_modules WHERE code = 'business.analisis';
    SELECT id INTO m_industry FROM tm_modules WHERE code = 'business.industry';
    SELECT id INTO m_admin FROM tm_modules WHERE code = 'business.administrasi';
    
    SELECT id INTO p_track FROM tm_modules WHERE code = 'personal.tracking';
    SELECT id INTO p_stat FROM tm_modules WHERE code = 'personal.statistics';
    SELECT id INTO p_set FROM tm_modules WHERE code = 'personal.settings';

    -- Menus for Business: Utama
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_utama, 'business.utama.dashboard', 'Dashboard', '/dashboard', 1),
    (m_utama, 'business.utama.live_map', 'Live Map', '/live-map', 2) ON CONFLICT (code) DO NOTHING;

    -- Menus for Business: Master Data
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_master, 'business.master.vehicles', 'Manajemen Kendaraan', '/master/vehicles', 1),
    (m_master, 'business.master.drivers', 'Manajemen Pengemudi', '/master/drivers', 2),
    (m_master, 'business.master.geofences', 'Manajemen Geofence', '/master/geofences', 3),
    (m_master, 'business.master.routes', 'Manajemen Rute', '/master/routes', 4) ON CONFLICT (code) DO NOTHING;

    -- Menus for Business: Keamanan
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (m_keamanan, 'business.security.alerts', 'Log Peringatan (Alerts)', '/security/alerts', 1),
    (m_keamanan, 'business.security.rules', 'Aturan Keamanan', '/security/rules', 2) ON CONFLICT (code) DO NOTHING;

    -- Menus for Personal: Tracking
    INSERT INTO tm_menus (module_id, code, name, path, sort_order) VALUES 
    (p_track, 'personal.tracking.map', 'My Vehicles Map', '/personal/map', 1),
    (p_track, 'personal.tracking.history', 'Trip History', '/personal/history', 2) ON CONFLICT (code) DO NOTHING;
END $$;


-- 4. SEED WILAYAH GEOGRAFIS (38 PROVINSI INDONESIA)
INSERT INTO tm_countries (code, name) VALUES ('ID', 'Indonesia') ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_provinces (country_code, name) VALUES 
('ID', 'Aceh'), ('ID', 'Sumatera Utara'), ('ID', 'Sumatera Barat'), ('ID', 'Riau'), 
('ID', 'Jambi'), ('ID', 'Sumatera Selatan'), ('ID', 'Bengkulu'), ('ID', 'Lampung'), 
('ID', 'Kepulauan Bangka Belitung'), ('ID', 'Kepulauan Riau'), ('ID', 'DKI Jakarta'), 
('ID', 'Jawa Barat'), ('ID', 'Jawa Tengah'), ('ID', 'DI Yogyakarta'), ('ID', 'Jawa Timur'), 
('ID', 'Banten'), ('ID', 'Bali'), ('ID', 'Nusa Tenggara Barat'), ('ID', 'Nusa Tenggara Timur'), 
('ID', 'Kalimantan Barat'), ('ID', 'Kalimantan Tengah'), ('ID', 'Kalimantan Selatan'), 
('ID', 'Kalimantan Timur'), ('ID', 'Kalimantan Utara'), ('ID', 'Sulawesi Utara'), 
('ID', 'Sulawesi Tengah'), ('ID', 'Sulawesi Selatan'), ('ID', 'Sulawesi Tenggara'), 
('ID', 'Gorontalo'), ('ID', 'Sulawesi Barat'), ('ID', 'Maluku'), ('ID', 'Maluku Utara'), 
('ID', 'Papua Barat'), ('ID', 'Papua'), ('ID', 'Papua Selatan'), ('ID', 'Papua Tengah'), 
('ID', 'Papua Pegunungan'), ('ID', 'Papua Barat Daya')
ON CONFLICT DO NOTHING;
-- (Kabupaten, Kecamatan, Desa sebanyak 83,000+ akan di-load via mekanisme import CSV worker terpisah agar tidak membebani initial script)


-- 5. SEED DEFAULT COMPANY & ADMIN TERPUSAT
INSERT INTO tm_companies (code, name, legal_name, tax_id, country_code, business_type)
VALUES ('DEFAULT', 'Adatrack System', 'PT Adatrack Teknologi', '00.000.000.0-000.000', 'ID', 'b2b')
ON CONFLICT (code) DO NOTHING;

-- SuperAdmin Default (Password: Admin@123)
INSERT INTO tm_users (email, password_hash, global_role, must_change_password) 
VALUES ('superadmin@adatrack.local', '$2a$12$e/M.q9oFq1hH4KqJv6T.7O2s3b5K5tE7xR8Wq8N/kCqP6D.LqF6Xy', 'SuperAdmin', false)
ON CONFLICT (email) DO NOTHING;
