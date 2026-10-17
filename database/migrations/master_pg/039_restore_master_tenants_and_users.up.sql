-- ==============================================================================
-- Migration 039: Ensure Master Companies, Roles, and Users are Restored
-- ==============================================================================

-- 0. Country
INSERT INTO adatrack_gps_master.tm_countries (code, name) VALUES ('ID', 'Indonesia') ON CONFLICT (code) DO NOTHING;

-- 1. Companies
INSERT INTO adatrack_gps_master.tm_companies (code, name, legal_name, country_code, timezone, business_type) VALUES
  ('DEFAULT', 'Adatrack System', 'PT Adatrack Teknologi', 'ID', 'UTC', 'b2b'),
  ('TESST001', 'PT Test Staging', 'PT Test Staging', 'ID', 'UTC', 'b2b'),
  ('BETEIN01', 'BENI TEST INDONESIA', 'BENI TEST INDONESIA', 'ID', 'UTC', 'b2b'),
  ('SIOTKE01', 'PT Sinergi Otoritas Keuangan', 'PT Sinergi Otoritas Keuangan', 'ID', 'UTC', 'b2b')
ON CONFLICT (code) DO UPDATE SET 
  name = EXCLUDED.name, 
  deleted_at = NULL;

-- 2. Master Users (keeping exact IDs to maintain foreign key integrity with tenant schemas)
INSERT INTO adatrack_gps_master.tm_users (id, email, password_hash, is_active, must_change_password, full_name) VALUES
  (1, 'superadmin@adatrack.local', '$2a$12$J0F95QJy60BX8BBLYehJ6uabwqzKf2Lrhc.oH/.azHkfAZbqv4qGu', true, false, 'Super Admin'),
  (15, 'admin@adatrack.local', '$2a$12$og.Is5bq/dB3LLXdEZs/auXTysq/Q7uQ4EqCSSoFrenm86v/PkYuW', true, false, 'Admin'),
  (17, 'admin2@adatrack.local', '$2a$12$z0g4qEbLP1CWD2O9yAflo.KGokBpCvliehcgoBMkRHM0sK4IMapfe', true, false, 'Admin 2'),
  (24, 'budibeni@gmail.com', '$2a$12$ucIDb0h/9op3kUPDFrhIde4dlnT9hqzHkKBAjC99kL7C35V9ntp62', true, false, 'Budi Beni'),
  (33, 'admin@tesst001.local', '$2a$12$f0WDgIy1iyLqC93Eqelgc.93kuftiQg.gTWPN3JC92.oOyW5qSRf.', true, false, 'Admin TESST001'),
  (38, 'admin@betein01.local', '$2a$12$ogLqlBJ22aII64CIjNBFmeeqzitgdkBBmYyXy0nIVVlAfFcW27972', true, false, 'Admin BETEIN01'),
  (39, 'budi@gmail.com', '$2a$12$4EmY./mDS2wi8CUVO8TIy.IWcgdGTnu0sf/NeVdmH91jXXQA1Uhj.', true, false, 'Budi'),
  (42, 'admin@siotke01.local', '$2a$12$6TzXHRVZG7blkSwIBQ6Mb.KBB.Q8zsNZ.omsBRTqNhNqQWISMX6N6', true, false, 'Admin SIOTKE01'),
  (141, 'driver.siotke@adatrack.id', '$2a$12$6TzXHRVZG7blkSwIBQ6Mb.KBB.Q8zsNZ.omsBRTqNhNqQWISMX6N6', true, false, 'Driver Siotke')
ON CONFLICT (id) DO UPDATE SET 
  email = EXCLUDED.email, 
  password_hash = EXCLUDED.password_hash, 
  full_name = EXCLUDED.full_name,
  is_active = true, 
  deleted_at = NULL;

SELECT setval('adatrack_gps_master.tm_users_id_seq', (SELECT COALESCE(MAX(id), 1) FROM adatrack_gps_master.tm_users));

-- 3. Default Roles for Companies
INSERT INTO adatrack_gps_master.tm_roles (code, name, is_system, permissions, company_code) VALUES
  ('SUPER_ADMIN', 'Super Admin', true, '["*"]'::jsonb, 'DEFAULT'),
  ('ADMIN', 'Admin', true, '["*"]'::jsonb, 'DEFAULT'),
  ('MANAGER', 'Manager', true, '["vehicles:read", "reports:read"]'::jsonb, 'DEFAULT'),
  ('DRIVER', 'Driver', true, '["vehicles:read"]'::jsonb, 'DEFAULT'),
  ('OPERATOR', 'Operator', true, '["vehicles:read", "alerts:read"]'::jsonb, 'DEFAULT'),
  ('CUSTOMER_SERVICE', 'Customer Service', true, '["users:read", "vehicles:read"]'::jsonb, 'DEFAULT'),
  ('ADMIN', 'Admin', true, '["*"]'::jsonb, 'TESST001'),
  ('ADMIN', 'Admin', true, '["*"]'::jsonb, 'BETEIN01'),
  ('ADMIN', 'Admin', true, '["*"]'::jsonb, 'SIOTKE01')
ON CONFLICT (code, company_code) DO NOTHING;

-- 4. Default user company access for DEFAULT schema
INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code, is_active)
SELECT 1, 'SUPER_ADMIN', true
ON CONFLICT (user_id, role_code) DO UPDATE SET is_active = true, deleted_at = NULL;
