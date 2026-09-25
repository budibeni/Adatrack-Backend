DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tm_user_company_access' AND column_name='role_override') THEN
        ALTER TABLE tm_user_company_access RENAME COLUMN role_override TO role_code;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='tm_role_menu_access' AND column_name='role') THEN
        ALTER TABLE tm_role_menu_access RENAME COLUMN role TO role_code;
    END IF;
END $$;

ALTER TABLE tm_user_company_access DROP CONSTRAINT IF EXISTS uq_user_role;
ALTER TABLE tm_user_company_access ADD CONSTRAINT uq_user_role UNIQUE (user_id, role_code);
