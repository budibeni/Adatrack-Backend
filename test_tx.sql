BEGIN;
SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'test@test.local';
SELECT 1 FROM adatrack_gps_default.tm_user_company_access WHERE user_id = 99999;
INSERT INTO adatrack_gps_default.tm_user_company_access (user_id, role_code, is_active) VALUES (9999, 'TEST', true);
COMMIT;
