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

-- Modules are now seeded via migration 034

-- Menus are now seeded via migration 034

INSERT INTO tm_countries (code, name) VALUES ('ID', 'Indonesia') ON CONFLICT (code) DO NOTHING;
-- Note: Provinces, Cities, Districts, Subdistricts are imported via Go batch worker.

INSERT INTO tm_companies (code, name, legal_name, tax_id, country_code, business_type)
VALUES ('DEFAULT', 'Adatrack System', 'PT Adatrack Teknologi', '00.000.000.0-000.000', 'ID', 'b2b')
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_roles (code, name, is_system, permissions) VALUES
('SUPER_ADMIN', 'Super Admin', true, '["*"]'::jsonb),
('ADMIN', 'Admin', true, '["*"]'::jsonb),
('MANAGER', 'Manager', true, '["vehicles:read", "reports:read"]'::jsonb),
('DRIVER', 'Driver', true, '["vehicles:read"]'::jsonb),
('OPERATOR', 'Operator', true, '["vehicles:read", "alerts:read"]'::jsonb),
('CUSTOMER_SERVICE', 'Customer Service', true, '["users:read", "vehicles:read"]'::jsonb)
ON CONFLICT (code) DO NOTHING;

INSERT INTO tm_users (email, password_hash, must_change_password) 
VALUES ('superadmin@adatrack.local', '$2a$12$e/M.q9oFq1hH4KqJv6T.7O2s3b5K5tE7xR8Wq8N/kCqP6D.LqF6Xy', false)
ON CONFLICT (email) DO NOTHING;
-- Create DEFAULT schema for super admins

INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code)
SELECT id, 'SUPER_ADMIN' FROM adatrack_gps_master.tm_users WHERE email = 'superadmin@adatrack.local'
ON CONFLICT (user_id, role_code) DO NOTHING;
