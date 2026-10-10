UPDATE tm_roles SET permissions = '["*"]'::jsonb WHERE code = 'ADMIN' AND is_system = true;
