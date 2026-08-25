-- ===========================================================================
-- Seed E2E F3 — data ujian verifikasi runtime fase F3
--   Admin  : admin / admin123
--   Vehicle: 864201040512345 (GT06_Test)
--   Geofence: "Zona Jakarta Pusat" (CIRCLE, center -6.2088 106.8456, r=1000m)
--   History: ~6000 titik telemetry dalam 24 jam terakhir (query playback)
-- ===========================================================================
SET NAMES utf8mb4;
SET SESSION cte_max_recursion_depth = 10000;

INSERT INTO users (username, email, password_hash, first_name, last_name, role, status, email_verified, mfa_enabled, locale)
VALUES ('admin', 'admin@ajbgps.local',
        '$2a$10$jjM89mAHA6qFcPiVIewjhu4OXiCR5JvgPexZ4OdFsKhUc3r4cFeM2',
        'Admin', 'User', 'ADMIN', 'ACTIVE', TRUE, FALSE, 'id');

INSERT INTO vehicles (imei, device_model, firmware_version, status, user_id)
VALUES ('864201040512345', 'GT06_Test', 'v1.0', 'ACTIVE', 1);

INSERT INTO user_vehicles (user_id, vehicle_id)
VALUES (1, 1);

INSERT INTO geofences (name, description, geofence_type, center_latitude,
                       center_longitude, radius_meters, vehicle_scope, status, trigger_on)
VALUES ('Zona Jakarta Pusat', 'geofence uji F3', 'CIRCLE',
        -6.2088, 106.8456, 1000, 'ALL', 'ACTIVE', 'BOTH');

-- History: ~6000 titik tersebar di ~25 jam terakhir (turun 15 detik per poin).
INSERT INTO telemetry_logs (imei, received_at, event_timestamp, latitude, longitude,
                            speed, heading, satellites, battery_level, processed)
WITH RECURSIVE seq AS (SELECT 0 AS n UNION ALL SELECT n + 1 FROM seq WHERE n < 5999)
SELECT '864201040512345',
       FROM_UNIXTIME(UNIX_TIMESTAMP(NOW()) - (5999 - n) * 15),
       FROM_UNIXTIME(UNIX_TIMESTAMP(NOW()) - (5999 - n) * 15),
       -6.2088 + 0.00012 * SIN(n * 0.37),
       106.8456 + 0.00012 * COS(n * 0.37),
       40 + MOD(n, 35),
       90 + MOD(n, 360),
       9,
       75,
       1
FROM seq;

-- Bersihkan state Redis agar deterministik (vehicle:state & geofence_state).
-- (opsional, dijalankan via redis-cli bila perlu)