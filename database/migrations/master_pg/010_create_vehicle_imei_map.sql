-- ============================================================================
-- Migration: MASTER 010 — tm_vehicle_imei_map (anti-spoofing / tenant resolution)
-- ============================================================================
-- The ONLY authority that maps a device IMEI to a tenant (PRD FR-1.4).
-- ingestion-tcp looks up every login here; unknown IMEIs are rejected + audited
-- (IMEI_REJECTED, §9.4) and counted in tenant_lookup_errors_total.
-- vehicle_id refers logically to the company schema's tm_vehicles.id (no
-- cross-schema FK — the schema name is dynamic).

CREATE TABLE IF NOT EXISTS tm_vehicle_imei_map (
    imei VARCHAR(30) PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tm_vehicle_imei_map_company FOREIGN KEY (company_code) REFERENCES tm_companies (code)
);
CREATE INDEX IF NOT EXISTS idx_tm_vehicle_imei_map_company ON tm_vehicle_imei_map (company_code);
CREATE INDEX IF NOT EXISTS idx_tm_vehicle_imei_map_vehicle ON tm_vehicle_imei_map (vehicle_id);
CREATE INDEX IF NOT EXISTS idx_tm_vehicle_imei_map_active ON tm_vehicle_imei_map (is_active);