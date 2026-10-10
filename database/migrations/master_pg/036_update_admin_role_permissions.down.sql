UPDATE tm_roles SET permissions = '["users:read", "users:write", "vehicles:read", "vehicles:write"]'::jsonb WHERE code = 'ADMIN' AND is_system = true;
