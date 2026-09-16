CREATE TABLE IF NOT EXISTS th_telemetry_logs (
    id BIGSERIAL,
    vehicle_id INT NOT NULL,
    imei VARCHAR(20) NOT NULL,
    company_code VARCHAR(50) NOT NULL,
    lat DECIMAL(10, 7) NOT NULL,
    lon DECIMAL(10, 7) NOT NULL,
    speed FLOAT NOT NULL,
    heading FLOAT,
    altitude FLOAT,
    acc_status SMALLINT NOT NULL,
    battery_level FLOAT,
    timestamp TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(id, timestamp)
) PARTITION BY RANGE (timestamp);
CREATE INDEX IF NOT EXISTS idx_telemetry_vehicle_ts ON th_telemetry_logs(vehicle_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_telemetry_imei_ts ON th_telemetry_logs(imei, timestamp DESC);
