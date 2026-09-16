-- ============================================================================
-- Migration: COMPANY 008 — tm_geofences + tm_geofence_vehicles (B3, PRD §5.9.1)
-- ============================================================================
-- circle  → center_lat/center_lon + radius_meters
-- polygon → boundary_points (JSON array of [lat, lon] pairs, first != last
--           required; the ring is closed implicitly) + optional GeoJSON in
--           `coordinates`. `coordinates` is informational (GeoJSON export);
--           detection uses radius_meters / boundary_points directly.

CREATE TABLE IF NOT EXISTS tm_geofences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(255),
    area_type VARCHAR(10) NOT NULL CHECK (area_type IN ('circle', 'polygon')),
    center_lat DECIMAL(10, 8),
    center_lon DECIMAL(11, 8),
    radius_meters INT,
    boundary_points JSONB,
    coordinates JSONB,
    severity VARCHAR(10) NOT NULL DEFAULT 'medium'
        CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    on_entry BOOLEAN NOT NULL DEFAULT TRUE,
    on_exit BOOLEAN NOT NULL DEFAULT TRUE,
    active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_geofences_name UNIQUE (name),
    CONSTRAINT ck_tm_geofences_circle CHECK (
        area_type <> 'circle' OR (center_lat IS NOT NULL AND center_lon IS NOT NULL AND radius_meters > 0)),
    CONSTRAINT ck_tm_geofences_polygon CHECK (
        area_type <> 'polygon' OR (boundary_points IS NOT NULL AND jsonb_array_length(boundary_points) >= 3))
);
CREATE INDEX IF NOT EXISTS idx_tm_geofences_active ON tm_geofences (active) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_geofences_deleted ON tm_geofences (deleted_at);

CREATE TABLE IF NOT EXISTS tm_geofence_vehicles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    geofence_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_geofence_vehicles UNIQUE (geofence_id, vehicle_id),
    CONSTRAINT fk_tm_geofence_vehicles_geofence FOREIGN KEY (geofence_id)
        REFERENCES tm_geofences (id) ON DELETE CASCADE,
    CONSTRAINT fk_tm_geofence_vehicles_vehicle FOREIGN KEY (vehicle_id)
        REFERENCES tm_vehicles (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_tm_geofence_vehicles_vehicle ON tm_geofence_vehicles (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_tm_geofence_vehicles_enabled ON tm_geofence_vehicles (enabled) WHERE deleted_at IS NULL;