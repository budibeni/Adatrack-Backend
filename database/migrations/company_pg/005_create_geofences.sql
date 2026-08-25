-- ============================================================================
-- Migration: COMPANY 005 — geofences (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS geofences (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    area_type TEXT NOT NULL CHECK (area_type IN ('circle','polygon')),
    coordinates JSONB NOT NULL,
    radius_meters INT,
    boundary_points JSONB,
    created_by BIGINT NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_gf_created FOREIGN KEY (created_by) REFERENCES user_company_access(user_id)
);
CREATE INDEX IF NOT EXISTS idx_gf_created ON geofences (created_by);
CREATE INDEX IF NOT EXISTS idx_gf_active ON geofences (is_active);

-- ============================================================================
-- End of COMPANY 005 (postgres)
-- ============================================================================