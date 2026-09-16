-- ============================================================================
-- Migration: COMPANY 010 — tm_routes + th_route_assignments (B3, PRD §5.9.2)
-- ============================================================================
-- waypoints: JSON array ordered by sequence: [{"seq":1,"lat":..,"lon":..,"name":".."}].
-- Assignment status machine (PRD §5.9.2):
--   not_started → in_progress → completed | delayed  (manual transition via API).

CREATE TABLE IF NOT EXISTS tm_routes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(255),
    waypoints JSONB NOT NULL,
    estimated_duration_min INT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_routes_name UNIQUE (name),
    CONSTRAINT ck_tm_routes_waypoints CHECK (jsonb_array_length(waypoints) >= 2)
);
CREATE INDEX IF NOT EXISTS idx_tm_routes_deleted ON tm_routes (deleted_at);

CREATE TABLE IF NOT EXISTS th_route_assignments (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    route_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    driver_user_id BIGINT,
    status VARCHAR(20) NOT NULL DEFAULT 'not_started'
        CHECK (status IN ('not_started', 'in_progress', 'completed', 'delayed')),
    deviation_meters DOUBLE PRECISION NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,

    assigned_by BIGINT,
    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_th_route_assignments_route FOREIGN KEY (route_id)
        REFERENCES tm_routes (id) ON DELETE CASCADE,
    CONSTRAINT fk_th_route_assignments_vehicle FOREIGN KEY (vehicle_id)
        REFERENCES tm_vehicles (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_th_route_assignments_route ON th_route_assignments (route_id);
CREATE INDEX IF NOT EXISTS idx_th_route_assignments_vehicle ON th_route_assignments (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_th_route_assignments_status ON th_route_assignments (status) WHERE deleted_at IS NULL;