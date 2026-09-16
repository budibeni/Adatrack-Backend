-- Seed basic reference data so the DB is usable immediately for tests
SET search_path TO adatrack_gps_master;

INSERT INTO tm_countries (code, name) VALUES ('ID', 'Indonesia') ON CONFLICT (code) DO NOTHING;
INSERT INTO tm_provinces (country_code, name) VALUES ('ID', 'DKI Jakarta'), ('ID', 'Jawa Barat') ON CONFLICT DO NOTHING;

-- Create default tenant in master
INSERT INTO tm_companies (code, name, legal_name, country_code, business_type)
VALUES ('DEFAULT', 'Default Company', 'PT Default', 'ID', 'b2b')
ON CONFLICT (code) DO NOTHING;

-- Seed default modules (FrontEnd alignment)
INSERT INTO tm_modules (code, name, app, sort_order) VALUES 
('business.tracking', 'Tracking', 'business', 1),
('business.master_data', 'Master Data', 'business', 2)
ON CONFLICT (code) DO NOTHING;

-- Default SuperAdmin (Password: Admin@123, bcrypt cost 12 generated offline for this seed)
-- $2a$12$e/M.q9oFq1hH4KqJv6T.7O2s3b5K5tE7xR8Wq8N/kCqP6D.LqF6Xy
INSERT INTO tm_users (email, password_hash, global_role, must_change_password) 
VALUES ('superadmin@adatrack.local', '$2a$12$e/M.q9oFq1hH4KqJv6T.7O2s3b5K5tE7xR8Wq8N/kCqP6D.LqF6Xy', 'SuperAdmin', false)
ON CONFLICT (email) DO NOTHING;
