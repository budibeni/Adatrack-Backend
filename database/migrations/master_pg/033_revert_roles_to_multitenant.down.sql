ALTER TABLE tm_roles DROP CONSTRAINT IF EXISTS tm_roles_code_company_key;
ALTER TABLE tm_roles ADD CONSTRAINT tm_roles_code_key UNIQUE (code);
ALTER TABLE tm_roles DROP COLUMN IF EXISTS company_code;
