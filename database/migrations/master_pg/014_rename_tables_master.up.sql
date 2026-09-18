-- Idempotent rename for legacy tables if they exist
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname='adatrack_gps_master' AND tablename='companies') THEN
        ALTER TABLE adatrack_gps_master.companies RENAME TO tm_companies;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname='adatrack_gps_master' AND tablename='users') THEN
        ALTER TABLE adatrack_gps_master.users RENAME TO tm_users;
    END IF;
    IF EXISTS (SELECT FROM pg_tables WHERE schemaname='adatrack_gps_master' AND tablename='users_b2c') THEN
        ALTER TABLE adatrack_gps_master.users_b2c RENAME TO tm_users_b2c;
    END IF;
END $$;
