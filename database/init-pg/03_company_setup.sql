-- ============================================================================
-- PostgreSQL bootstrap 03 — company schema template + dev tenant seeds
-- ============================================================================
-- Executed by the docker entrypoint after 02b-seed-reference.sh.
-- Applies EVERY company migration to the platform tenant (adatrack_gps_default)
-- and to the dev tenant (adatrack_gps_dev001), then seeds dev RBAC/vehicle data.
--
-- New tenants are provisioned later by either:
--   * scripts/provision-tenant.sh <CODE>          (ops/CLI)
--   * POST /api/v1/companies (SuperAdmin, FR-5.5) (B2, auto-apply same dir)
-- Both reuse the exact same migration directory → a tenant created any way ends
-- up structurally identical to the template below.
-- ============================================================================

-- Path/variable setup (see 02_master_setup.sql for the rationale).
\if :{?migrations_dir}
\else
\set migrations_dir /db/migrations
\endif

-- ---------------------------------------------------------------------------
-- Platform tenant (adatrack_gps_default)
-- ---------------------------------------------------------------------------
SET search_path TO adatrack_gps_default;

\if :{?skip_migrations}
\echo '03_company_setup: company migrations already applied by the caller — skipping \i blocks'
\else
\i :migrations_dir/company_pg/001_create_schema_migrations.sql
\i :migrations_dir/company_pg/002_create_user_company_access.sql
\i :migrations_dir/company_pg/003_create_role_menu_access.sql
\i :migrations_dir/company_pg/004_seed_role_menu_access.sql
\i :migrations_dir/company_pg/005_create_vehicles.sql
\i :migrations_dir/company_pg/006_create_user_vehicles.sql
\i :migrations_dir/company_pg/007_create_telemetry_logs.sql
\i :migrations_dir/company_pg/008_create_geofences.sql
\i :migrations_dir/company_pg/009_create_speed_configs.sql
\i :migrations_dir/company_pg/010_create_routes.sql
\i :migrations_dir/company_pg/011_create_alerts.sql
\i :migrations_dir/company_pg/012_create_notification_preferences.sql
\i :migrations_dir/company_pg/013_create_fuel_configs.sql
\i :migrations_dir/company_pg/014_create_fuel_logs.sql
\i :migrations_dir/company_pg/015_create_media_events.sql
\i :migrations_dir/company_pg/016_media_events_governance.sql
\i :migrations_dir/company_pg/017_add_telemetry_timestamp_index.sql
\i :migrations_dir/company_pg/018_create_odometer_engine_hours.sql
\i :migrations_dir/company_pg/019_create_vehicle_trips.sql
\i :migrations_dir/company_pg/020_telemetry_acc_nullable.sql


-- ---------------------------------------------------------------------------
-- Dev tenant (adatrack_gps_dev001)
-- ---------------------------------------------------------------------------
SET search_path TO adatrack_gps_dev001;

\i :migrations_dir/company_pg/001_create_schema_migrations.sql
\i :migrations_dir/company_pg/002_create_user_company_access.sql
\i :migrations_dir/company_pg/003_create_role_menu_access.sql
\i :migrations_dir/company_pg/004_seed_role_menu_access.sql
\i :migrations_dir/company_pg/005_create_vehicles.sql
\i :migrations_dir/company_pg/006_create_user_vehicles.sql
\i :migrations_dir/company_pg/007_create_telemetry_logs.sql
\i :migrations_dir/company_pg/008_create_geofences.sql
\i :migrations_dir/company_pg/009_create_speed_configs.sql
\i :migrations_dir/company_pg/010_create_routes.sql
\i :migrations_dir/company_pg/011_create_alerts.sql
\i :migrations_dir/company_pg/012_create_notification_preferences.sql
\i :migrations_dir/company_pg/013_create_fuel_configs.sql
\i :migrations_dir/company_pg/014_create_fuel_logs.sql
\i :migrations_dir/company_pg/015_create_media_events.sql
\i :migrations_dir/company_pg/016_media_events_governance.sql
\i :migrations_dir/company_pg/017_add_telemetry_timestamp_index.sql
\i :migrations_dir/company_pg/018_create_odometer_engine_hours.sql
\i :migrations_dir/company_pg/019_create_vehicle_trips.sql
\i :migrations_dir/company_pg/020_telemetry_acc_nullable.sql
\endif

-- ---------------------------------------------------------------------------
-- Dev tenant seeds (safe to run in both paths: the tables exist either way)
-- ---------------------------------------------------------------------------
SET search_path TO adatrack_gps_dev001;

-- -- -- Dev RBAC rows (resolved by email so ids are never hardcoded — ids are NOT
-- stable across environments and the platform SuperAdmin (master 019) must NEVER
-- gain tenant membership; PRD §3.1 scope guard, anti privilege-escalation). -- --
INSERT INTO tm_user_company_access (user_id, role_override, is_active) VALUES
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'admin@dev001.io'),  'Admin',    TRUE),
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'operator@dev001.io'), 'Operator', TRUE),
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'driver@dev001.io'),  'Driver',   TRUE)
ON CONFLICT (user_id) DO UPDATE SET
    role_override = EXCLUDED.role_override,
    is_active = EXCLUDED.is_active;

-- -- -- Dev fleet (IMEIs are registered in master.tm_vehicle_imei_map) -- -- --
INSERT INTO tm_vehicles (id, imei, plate_number, make, model, variant, year_of_manufacture,
                         engine_number, chassis_number, color, fuel_type,
                         vehicle_category_code, vehicle_type_code, driver_name,
                         registration_number, registration_expiry, insurance_number, insurance_expiry,
                         road_tax_expiry, inspection_expiry,
                         gross_vehicle_weight, payload_capacity, vehicle_length, vehicle_width, vehicle_height,
                         status)
OVERRIDING SYSTEM VALUE
VALUES
    (1, '864201040512345', 'B 1234 XYZ', 'Toyota', 'Hilux', 'G', 2022,
     '1GR-FE12345', 'JTMBFREV20D123456', 'Silver', 'diesel', 'LCV', 'PICKUP_TRUCK', 'Test Driver A',
     'B 1234 XYZ', '2027-12-31', 'AS-7890123', '2026-06-30', '2026-12-31', '2026-05-15',
     3100, 1200, 4625, 1780, 1720, 'active'),
    (2, '864201040512346', 'D 5678 ABC', 'Hanwha', 'HD65', 'Standard', 2021,
     'H65E1234567', 'KMACKCD06E1234567', 'White', 'diesel', 'HCV', 'MEDIUM_TRUCK', 'Test Driver B',
     'D 5678 ABC', '2028-06-30', 'TS-4567890', '2027-01-31', '2027-06-30', '2026-11-30',
     8500, 3500, 6500, 2300, 2500, 'active'),
    (3, '864201040512347', 'E 9012 RST', 'Honda', 'Civic', 'VX', 2023,
     'HONC1234567', 'SHSKE2600M8012345', 'Black', 'petrol', 'PVB', 'SEDAN', 'Test Driver C',
     'E 9012 RST', '2027-03-31', 'AS-1122334', '2026-08-15', '2026-12-15', '2026-07-30',
     1450, 450, 4633, 1799, 1433, 'active')
ON CONFLICT (id) DO UPDATE SET
    imei = EXCLUDED.imei,
    plate_number = EXCLUDED.plate_number,
    status = EXCLUDED.status;

SELECT setval(pg_get_serial_sequence('tm_vehicles', 'id'),
              GREATEST((SELECT COALESCE(MAX(id), 1) FROM tm_vehicles), 3));

-- Operator gets vehicles 1-2, driver gets vehicle 3 (row-level RBAC fixtures).
-- Resolved by email so ids are never hardcoded (ids are not stable across
-- environments; PRD §3.1 scope guard).
INSERT INTO tm_user_vehicles (user_id, vehicle_id) VALUES
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'operator@dev001.io'), 1),
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'operator@dev001.io'), 2),
    ((SELECT id FROM adatrack_gps_master.tm_users WHERE email = 'driver@dev001.io'),  3)
ON CONFLICT (user_id, vehicle_id) DO NOTHING;