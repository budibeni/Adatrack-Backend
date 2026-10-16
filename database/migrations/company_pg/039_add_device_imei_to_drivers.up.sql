ALTER TABLE tm_drivers ADD COLUMN IF NOT EXISTS device_imei VARCHAR(50);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_drivers_device_imei ON tm_drivers (device_imei) WHERE device_imei IS NOT NULL AND deleted_at IS NULL;
