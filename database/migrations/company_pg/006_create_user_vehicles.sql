-- ============================================================================
-- Migration: COMPANY 006 — tm_user_vehicles (row-level RBAC junction, PRD §6.2)
-- ============================================================================
-- Grants a user access to specific vehicles (row-level security for
-- Operator/Driver; Admin sees every vehicle via role, not via this table).
-- ON DELETE CASCADE applies to the HARD delete retention job only — normal
-- operations always soft delete (§6.0.1).

CREATE TABLE IF NOT EXISTS tm_user_vehicles (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id BIGINT NOT NULL,
    vehicle_id BIGINT NOT NULL,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT uq_tm_user_vehicles UNIQUE (user_id, vehicle_id),
    CONSTRAINT fk_tm_user_vehicles_vehicle FOREIGN KEY (vehicle_id)
        REFERENCES tm_vehicles (id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_tm_user_vehicles_user ON tm_user_vehicles (user_id);
CREATE INDEX IF NOT EXISTS idx_tm_user_vehicles_vehicle ON tm_user_vehicles (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_tm_user_vehicles_deleted ON tm_user_vehicles (deleted_at);