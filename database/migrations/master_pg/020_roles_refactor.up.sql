ALTER TABLE tm_roles DROP COLUMN IF EXISTS company_code CASCADE;
DELETE FROM tm_roles WHERE id NOT IN (
    SELECT MIN(id) FROM tm_roles GROUP BY code
);
ALTER TABLE tm_roles ADD CONSTRAINT tm_roles_code_key UNIQUE (code);
