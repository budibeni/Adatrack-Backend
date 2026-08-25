-- ============================================================================
-- Migration: COMPANY 003 — user_vehicles (RBAC mapping, PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS user_vehicles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,
    assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT uq_user_vehicle UNIQUE (user_id, vehicle_id),
    CONSTRAINT fk_uv_user FOREIGN KEY (user_id) REFERENCES user_company_access(user_id) ON DELETE CASCADE,
    CONSTRAINT fk_uv_vehicle FOREIGN KEY (vehicle_id) REFERENCES vehicles(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_uv_user ON user_vehicles (user_id);
CREATE INDEX IF NOT EXISTS idx_uv_vehicle ON user_vehicles (vehicle_id);

-- ============================================================================
-- End of COMPANY 003 (postgres)
-- ============================================================================