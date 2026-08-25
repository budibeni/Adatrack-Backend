-- ============================================================================
-- Migration: COMPANY 012 — route_assignments (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS route_assignments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    route_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    driver_user_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'not_started'
        CHECK (status IN ('not_started','in_progress','completed','delayed')),
    started_at TIMESTAMP,
    completed_at TIMESTAMP,
    deviation_meters DOUBLE PRECISION DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_ra_driver FOREIGN KEY (driver_user_id) REFERENCES user_company_access(user_id),
    CONSTRAINT fk_ra_route FOREIGN KEY (route_id) REFERENCES routes(id),
    CONSTRAINT fk_ra_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id)
);
CREATE INDEX IF NOT EXISTS idx_ra_route ON route_assignments (route_id);
CREATE INDEX IF NOT EXISTS idx_ra_vehicle ON route_assignments (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_ra_status ON route_assignments (status);
CREATE INDEX IF NOT EXISTS idx_ra_driver ON route_assignments (driver_user_id);

-- ============================================================================
-- End of COMPANY 012 (postgres)
-- ============================================================================