WITH users AS (
  SELECT 9999 as id
)
SELECT user_id, 'DEFAULT' as company_code FROM adatrack_gps_default.tm_user_company_access WHERE user_id = ANY(ARRAY[9999]) AND deleted_at IS NULL AND is_active = true
UNION ALL
SELECT user_id, 'LIALSE01' as company_code FROM adatrack_gps_lialse01.tm_user_company_access WHERE user_id = ANY(ARRAY[9999]) AND deleted_at IS NULL AND is_active = true;
