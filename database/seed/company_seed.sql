-- ============================================================================
-- SEED — COMPANY DB (dipakai untuk adatrack_gps_default & adatrack_gps_dev001) — dev only
-- ============================================================================
-- Sample vehicle + speed config agar pipeline B1 & dashboard bisa langsung diuji.
-- Vehicle id di-sync HARDCODE (1, 2, 3) supaya vehicle_imei_map.vehicle_id di master
-- konsisten (device: 1, 2, 3). Memakai field enterprise-standard (make, model, VIN,
-- fuel_type, category/type code, registration, dimensi, dst.).
-- ============================================================================

-- 1) Sample vehicles (enterprise-standard fields)
INSERT INTO vehicles (
    id, imei, plate_number, make, model, variant, year_of_manufacture,
    engine_number, chassis_number, color, fuel_type,
    vehicle_category_code, vehicle_type_code,
    vehicle_type, driver_name, driver_user_id, device_model, firmware_version,
    registration_number, registration_expiry, insurance_number, insurance_expiry,
    road_tax_expiry, inspection_expiry,
    gross_vehicle_weight, payload_capacity, vehicle_length, vehicle_width, vehicle_height,
    status
) VALUES
    (1, '864201040512345', 'B 1234 XYZ',
     'Toyota', 'Hilux', 'G', 2022,
     '1GR-FE12345', 'JTMBFREV20D123456', 'Silver', 'diesel',
     'LCV', 'PICKUP_TRUCK',
     'truck', 'Test Driver A', NULL, 'GT06', 'V3.4',
     'B 1234 XYZ', '2027-12-31', 'AS-7890123', '2026-06-30',
     '2026-12-31', '2026-05-15',
     3100, 1200, 4625, 1780, 1720,
     'active'),
    (2, '864201040512346', 'D 5678 ABC',
     'Hanwha', 'HD65', 'Standard', 2021,
     'H65E1234567', 'KMACKCD06E1234567', 'White', 'diesel',
     'HCV', 'MEDIUM_TRUCK',
     'van', 'Test Driver B', NULL, 'Teltonika FMB920', 'FW3.7.1',
     'D 5678 ABC', '2028-06-30', 'TS-4567890', '2027-01-31',
     '2027-06-30', '2026-11-30',
     8500, 3500, 6500, 2300, 2500,
     'active'),
    (3, '864201040512347', 'E 9012 RST',
     'Honda', 'Civic', 'VX', 2023,
     'HONC1234567', 'SHSKE2600M8012345', 'Black', 'petrol',
     'PVB', 'SEDAN',
     'sedan', 'Test Driver C', NULL, 'GT06', 'V3.4',
     'E 9012 RST', '2027-03-31', 'AS-1122334', '2026-08-15',
     '2026-12-15', '2026-07-30',
     1450, 450, 4633, 1799, 1433,
     'active')
ON DUPLICATE KEY UPDATE
    plate_number           = VALUES(plate_number),
    make                   = VALUES(make),
    model                  = VALUES(model),
    variant                = VALUES(variant),
    year_of_manufacture    = VALUES(year_of_manufacture),
    engine_number          = VALUES(engine_number),
    chassis_number         = VALUES(chassis_number),
    color                  = VALUES(color),
    fuel_type              = VALUES(fuel_type),
    vehicle_category_code  = VALUES(vehicle_category_code),
    vehicle_type_code      = VALUES(vehicle_type_code),
    vehicle_type           = VALUES(vehicle_type),
    driver_name            = VALUES(driver_name),
    driver_user_id         = VALUES(driver_user_id),
    device_model           = VALUES(device_model),
    firmware_version       = VALUES(firmware_version),
    registration_number    = VALUES(registration_number),
    registration_expiry    = VALUES(registration_expiry),
    insurance_number       = VALUES(insurance_number),
    insurance_expiry       = VALUES(insurance_expiry),
    road_tax_expiry        = VALUES(road_tax_expiry),
    inspection_expiry      = VALUES(inspection_expiry),
    gross_vehicle_weight   = VALUES(gross_vehicle_weight),
    payload_capacity       = VALUES(payload_capacity),
    vehicle_length         = VALUES(vehicle_length),
    vehicle_width          = VALUES(vehicle_width),
    vehicle_height         = VALUES(vehicle_height),
    status                 = VALUES(status);

-- 2) Global default speed configuration (vehicle_id NULL = default)
INSERT INTO speed_configs (vehicle_id, speed_limit_kmh, grace_margin_kmh, is_active) VALUES
    (NULL, 80, 10, TRUE);

-- 3) Default notification preference template untuk websocket (severity warning+)
INSERT INTO notification_preferences (user_id, alert_type, channel, min_severity, is_enabled)
SELECT uca.user_id, 'geofence', 'websocket', 'warning', TRUE FROM user_company_access uca
ON DUPLICATE KEY UPDATE is_enabled = VALUES(is_enabled);

-- ===========================================================================
-- End of Company Seed
-- ============================================================================
-- ============================================================================
# SEED B2 (appended) — RBAC company DEV001: operator (vehicles 1,2), driver (3),
# geofence contoh + alert contoh untuk E2E B2.
-- ============================================================================
-- B2 seed: user_company_access + user_vehicles + geofence + alert (DEV001)
INSERT INTO user_company_access (user_id, role_override, is_active) VALUES
    (1, 'Admin', TRUE),
    (3, 'Operator', TRUE),
    (4, 'Driver', TRUE)
ON DUPLICATE KEY UPDATE role_override = VALUES(role_override), is_active = VALUES(is_active);

INSERT INTO user_vehicles (user_id, vehicle_id) VALUES
    (3, 1), (3, 2), (4, 3)
ON DUPLICATE KEY UPDATE user_id = VALUES(user_id);

INSERT INTO geofences (name, area_type, coordinates, radius_meters, boundary_points, created_by)
SELECT 'Depot Jakarta', 'circle', JSON_OBJECT('type','Point','coordinates',JSON_ARRAY(106.8456,-6.2088)), 800, NULL, MIN(id)
FROM user_company_access
ON DUPLICATE KEY UPDATE name = VALUES(name);

INSERT IGNORE INTO geofence_vehicles (geofence_id, vehicle_id)
SELECT g.id, v.id FROM geofences g CROSS JOIN vehicles v WHERE g.name = 'Depot Jakarta' AND v.id IN (1,2,3);

INSERT INTO alerts (vehicle_id, alert_type, severity, description, status, vehicle_lat, vehicle_lon)
VALUES (1, 'OVERSPEEDING', 'high', 'Over speed sample (B2)', 'open', -6.2088, 106.8456);
