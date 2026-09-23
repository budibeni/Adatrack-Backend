ALTER TABLE tm_user_company_access RENAME COLUMN role_override TO role_code;
ALTER TABLE tm_role_menu_access RENAME COLUMN role TO role_code;
ALTER TABLE tm_user_company_access ADD CONSTRAINT uq_user_role UNIQUE (user_id, role_code);
