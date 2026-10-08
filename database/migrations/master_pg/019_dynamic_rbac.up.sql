CREATE TABLE IF NOT EXISTS tm_roles (
    id SERIAL PRIMARY KEY,
    company_code VARCHAR(50) REFERENCES tm_companies(code),
    code VARCHAR(50) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    is_system BOOLEAN DEFAULT false,
    permissions JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(company_code, code)
);
ALTER TABLE tm_users DROP COLUMN IF EXISTS global_role;
ALTER TABLE tm_users_b2c DROP COLUMN IF EXISTS role;

INSERT INTO tm_roles (company_code, code, name, is_system, permissions) VALUES
(NULL, 'SUPER_ADMIN', 'Super Admin', true, '["*"]'::jsonb),
(NULL, 'ADMIN', 'Admin', true, '["*"]'::jsonb),
(NULL, 'MANAGER', 'Manager', true, '["vehicles:read", "vehicles:write", "routes:read", "routes:write", "geofences:read", "geofences:write", "alerts:read", "reports:read", "users:read"]'::jsonb),
(NULL, 'DRIVER', 'Driver', true, '["vehicles:read", "routes:read"]'::jsonb),
(NULL, 'OPERATOR', 'Operator', true, '["vehicles:read", "routes:read", "alerts:read", "reports:read"]'::jsonb),
(NULL, 'CUSTOMER_SERVICE', 'Customer Service', true, '["users:read", "vehicles:read"]'::jsonb)
ON CONFLICT (company_code, code) DO UPDATE SET permissions = EXCLUDED.permissions;
