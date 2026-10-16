DROP INDEX IF EXISTS uq_tm_drivers_device_imei;
ALTER TABLE tm_drivers DROP COLUMN IF EXISTS device_imei;
