-- ============================================================================
-- Migration: COMPANY 008 — alerts (PostgreSQL)
-- ============================================================================
-- Lifecycle: open → acknowledged → resolved.
-- ============================================================================

CREATE TABLE IF NOT EXISTS alerts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    vehicle_id BIGINT NOT NULL,
    alert_type TEXT NOT NULL
        CHECK (alert_type IN ('GEOFENCE_BREACH','OVERSPEEDING','OFFLINE','BATTERY_LOW','SOS','ROUTE_DEVIATION','FUEL_DROP','REFUEL')),
    severity TEXT NOT NULL DEFAULT 'medium'
        CHECK (severity IN ('low','medium','high','critical')),
    description TEXT,
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved')),
    acknowledged_by BIGINT,
    acknowledged_at TIMESTAMP,
    resolved_at TIMESTAMP,
    vehicle_lat DECIMAL(10, 8),
    vehicle_lon DECIMAL(11, 8),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_alt_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id),
    CONSTRAINT fk_alt_ack FOREIGN KEY (acknowledged_by) REFERENCES user_company_access(user_id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_alt_vehicle ON alerts (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_alt_status ON alerts (status);
CREATE INDEX IF NOT EXISTS idx_alt_type ON alerts (alert_type);
CREATE INDEX IF NOT EXISTS idx_alt_created ON alerts (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alt_severity ON alerts (severity);

-- ============================================================================
-- End of COMPANY 008 (postgres)
-- ============================================================================