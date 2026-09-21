ALTER TABLE tm_roles DROP CONSTRAINT IF EXISTS tm_roles_code_key;
ALTER TABLE tm_roles ADD COLUMN company_code VARCHAR(50);
