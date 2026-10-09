ALTER TABLE tm_roles ADD COLUMN company_code VARCHAR(50) REFERENCES tm_companies(code);
ALTER TABLE tm_roles DROP CONSTRAINT IF EXISTS tm_roles_code_key;
ALTER TABLE tm_roles ADD CONSTRAINT tm_roles_code_company_key UNIQUE (code, company_code);
