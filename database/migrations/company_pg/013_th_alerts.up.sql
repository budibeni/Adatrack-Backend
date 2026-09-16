CREATE TABLE IF NOT EXISTS th_alerts (
    id BIGSERIAL PRIMARY KEY,
    type VARCHAR(50) NOT NULL,
    severity VARCHAR(20) NOT NULL,
    vehicle_id INT NOT NULL,
    lat DECIMAL(10, 7),
    lon DECIMAL(10, 7),
    metadata JSONB,
    status VARCHAR(20) DEFAULT 'open',
    acknowledged_by INT,
    resolved_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
