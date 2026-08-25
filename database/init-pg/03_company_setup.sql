-- ============================================================================
-- PostgreSQL init (03): Company setup — apply company_pg migrations + seed
-- ============================================================================
-- Menerapkan migration company_pg (001-012) ke schema tiap tenant dev
-- (adatrack_gps_default & adatrack_gps_dev001) + seed minimal (vehicles, RBAC,
-- speed config, geofence) — mirror company_seed.sql MySQL.
-- ============================================================================

-- ---------------------------------------------------------------------------
-- Tenant: adatrack_gps_default
-- ---------------------------------------------------------------------------
SET search_path TO adatrack_gps_default;

\i /db/migrations/company_pg/001_create_user_company_access.sql
\i /db/migrations/company_pg/002_create_vehicles.sql
\i /db/migrations/company_pg/003_create_user_vehicles.sql
\i /db/migrations/company_pg/004_create_telemetry_logs.sql
\i /db/migrations/company_pg/005_create_geofences.sql
\i /db/migrations/company_pg/006_create_geofence_vehicles.sql
\i /db/migrations/company_pg/007_create_speed_configs.sql
\i /db/migrations/company_pg/008_create_alerts.sql
\i /db/migrations/company_pg/009_create_notification_preferences.sql
\i /db/migrations/company_pg/010_create_notifications.sql
\i /db/migrations/company_pg/011_create_routes.sql
\i /db/migrations/company_pg/012_create_route_assignments.sql

-- Seed default: operator (3) & driver (4) dari master + 3 vehicle sample
INSERT INTO user_company_access (user_id, role_override, is_active) VALUES
    (3, 'Operator', TRUE),
    (4, 'Driver', TRUE)
ON CONFLICT (user_id) DO UPDATE SET role_override = EXCLUDED.role_override, is_active = EXCLUDED.is_active;

INSERT INTO vehicles (id, imei, plate_number, make, model, variant, year_of_manufacture,
                      engine_number, chassis_number, color, fuel_type,
                      vehicle_category_code, vehicle_type_code, vehicle_type, driver_name,
                      registration_number, registration_expiry, insurance_number, insurance_expiry,
                      road_tax_expiry, inspection_expiry,
                      gross_vehicle_weight, payload_capacity, vehicle_length, vehicle_width, vehicle_height,
                      status)
VALUES
    (1, '864201040512345', 'B 1234 XYZ', 'Toyota', 'Hilux', 'G', 2022,
     '1GR-FE12345', 'JTMBFREV20D123456', 'Silver', 'diesel', 'LCV', 'PICKUP_TRUCK', 'truck', 'Test Driver A',
     'B 1234 XYZ', '2027-12-31', 'AS-7890123', '2026-06-30', '2026-12-31', '2026-05-15',
     3100, 1200, 4625, 1780, 1720, 'active'),
    (2, '864201040512346', 'D 5678 ABC', 'Hanwha', 'HD65', 'Standard', 2021,
     'H65E1234567', 'KMACKCD06E1234567', 'White', 'diesel', 'HCV', 'MEDIUM_TRUCK', 'van', 'Test Driver B',
     'D 5678 ABC', '2028-06-30', 'TS-4567890', '2027-01-31', '2027-06-30', '2026-11-30',
     8500, 3500, 6500, 2300, 2500, 'active'),
    (3, '864201040512347', 'E 9012 RST', 'Honda', 'Civic', 'VX', 2023,
     'HONC1234567', 'SHSKE2600M8012345', 'Black', 'petrol', 'PVB', 'SEDAN', 'sedan', 'Test Driver C',
     'E 9012 RST', '2027-03-31', 'AS-1122334', '2026-08-15', '2026-12-15', '2026-07-30',
     1450, 450, 4633, 1799, 1433, 'active')
ON CONFLICT (id) DO UPDATE SET
    plate_number = EXCLUDED.plate_number, status = EXCLUDED.status;

SELECT setval(pg_get_serial_sequence('vehicles', 'id'), GREATEST((SELECT COALESCE(MAX(id),1) FROM vehicles), 3));

INSERT INTO user_vehicles (user_id, vehicle_id) VALUES
    (3, 1), (3, 2), (4, 3)
ON CONFLICT (user_id, vehicle_id) DO NOTHING;

INSERT INTO speed_configs (vehicle_id, speed_limit_kmh, grace_margin_kmh, is_active) VALUES
    (NULL, 80, 10, TRUE);

INSERT INTO geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by)
SELECT 'Depot Jakarta', 'circle',
       ('{"type":"Point","coordinates":[' || 106.8456 || ',' || -6.2088 || ']}')::jsonb,
       800, NULL, (SELECT user_id FROM user_company_access ORDER BY user_id LIMIT 1)
ON CONFLICT DO NOTHING;

INSERT INTO geofence_vehicles (geofence_id, vehicle_id)
SELECT g.id, v.id FROM geofences g CROSS JOIN vehicles v WHERE g.name = 'Depot Jakarta' AND v.id IN (1,2,3)
ON CONFLICT DO NOTHING;

-- ---------------------------------------------------------------------------
-- Tenant: adatrack_gps_dev001
-- ---------------------------------------------------------------------------
SET search_path TO adatrack_gps_dev001;

\i /db/migrations/company_pg/001_create_user_company_access.sql
\i /db/migrations/company_pg/002_create_vehicles.sql
\i /db/migrations/company_pg/003_create_user_vehicles.sql
\i /db/migrations/company_pg/004_create_telemetry_logs.sql
\i /db/migrations/company_pg/005_create_geofences.sql
\i /db/migrations/company_pg/006_create_geofence_vehicles.sql
\i /db/migrations/company_pg/007_create_speed_configs.sql
\i /db/migrations/company_pg/008_create_alerts.sql
\i /db/migrations/company_pg/009_create_notification_preferences.sql
\i /db/migrations/company_pg/010_create_notifications.sql
\i /db/migrations/company_pg/011_create_routes.sql
\i /db/migrations/company_pg/012_create_route_assignments.sql

INSERT INTO user_company_access (user_id, role_override, is_active) VALUES
    (1, 'Admin', TRUE),
    (3, 'Operator', TRUE),
    (4, 'Driver', TRUE)
ON CONFLICT (user_id) DO UPDATE SET role_override = EXCLUDED.role_override, is_active = EXCLUDED.is_active;

-- Seed dev001: 3 vehicle sample (vehicle_imei_map di master sudah mengarah ke sini)
INSERT INTO vehicles (id, imei, plate_number, make, model, variant, year_of_manufacture,
                      engine_number, chassis_number, color, fuel_type,
                      vehicle_category_code, vehicle_type_code, vehicle_type, driver_name,
                      registration_number, registration_expiry, insurance_number, insurance_expiry,
                      road_tax_expiry, inspection_expiry,
                      gross_vehicle_weight, payload_capacity, vehicle_length, vehicle_width, vehicle_height,
                      status)
VALUES
    (1, '864201040512345', 'B 1234 XYZ', 'Toyota', 'Hilux', 'G', 2022,
     '1GR-FE12345', 'JTMBFREV20D123456', 'Silver', 'diesel', 'LCV', 'PICKUP_TRUCK', 'truck', 'Test Driver A',
     'B 1234 XYZ', '2027-12-31', 'AS-7890123', '2026-06-30', '2026-12-31', '2026-05-15',
     3100, 1200, 4625, 1780, 1720, 'active'),
    (2, '864201040512346', 'D 5678 ABC', 'Hanwha', 'HD65', 'Standard', 2021,
     'H65E1234567', 'KMACKCD06E1234567', 'White', 'diesel', 'HCV', 'MEDIUM_TRUCK', 'van', 'Test Driver B',
     'D 5678 ABC', '2028-06-30', 'TS-4567890', '2027-01-31', '2027-06-30', '2026-11-30',
     8500, 3500, 6500, 2300, 2500, 'active'),
    (3, '864201040512347', 'E 9012 RST', 'Honda', 'Civic', 'VX', 2023,
     'HONC1234567', 'SHSKE2600M8012345', 'Black', 'petrol', 'PVB', 'SEDAN', 'sedan', 'Test Driver C',
     'E 9012 RST', '2027-03-31', 'AS-1122334', '2026-08-15', '2026-12-15', '2026-07-30',
     1450, 450, 4633, 1799, 1433, 'active')
ON CONFLICT (id) DO UPDATE SET
    plate_number = EXCLUDED.plate_number, status = EXCLUDED.status;

SELECT setval(pg_get_serial_sequence('vehicles', 'id'), GREATEST((SELECT COALESCE(MAX(id),1) FROM vehicles), 3));

INSERT INTO user_vehicles (user_id, vehicle_id) VALUES
    (3, 1), (3, 2), (4, 3)
ON CONFLICT (user_id, vehicle_id) DO NOTHING;

INSERT INTO speed_configs (vehicle_id, speed_limit_kmh, grace_margin_kmh, is_active) VALUES
    (NULL, 80, 10, TRUE);

INSERT INTO geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by)
SELECT 'Depot Jakarta', 'circle',
       ('{"type":"Point","coordinates":[' || 106.8456 || ',' || -6.2088 || ']}')::jsonb,
       800, NULL, (SELECT user_id FROM user_company_access ORDER BY user_id LIMIT 1)
ON CONFLICT DO NOTHING;

INSERT INTO geofence_vehicles (geofence_id, vehicle_id)
SELECT g.id, v.id FROM geofences g CROSS JOIN vehicles v WHERE g.name = 'Depot Jakarta' AND v.id IN (1,2,3)
ON CONFLICT DO NOTHING;

-- ============================================================================
-- End of init-pg/03_company_setup.sql
-- ============================================================================