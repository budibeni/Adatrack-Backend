-- Create DEFAULT schema for super admins
CREATE SCHEMA IF NOT EXISTS adatrack_gps_default;

CREATE TABLE IF NOT EXISTS adatrack_gps_default.tm_user_company_access (
    id SERIAL PRIMARY KEY,
    user_id INT REFERENCES adatrack_gps_master.tm_users(id),
    role_code VARCHAR(50),
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE,
    deleted_by INT,
    UNIQUE(user_id, role_code)
);

INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code)
SELECT id, 'SUPER_ADMIN' FROM adatrack_gps_master.tm_users WHERE email = 'superadmin@adatrack.local'
ON CONFLICT (user_id, role_code) DO NOTHING;
