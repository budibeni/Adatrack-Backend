-- ============================================================================
-- Migration: MASTER 008 — vehicle_imei_map (PostgreSQL)
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_imei_map (
    imei VARCHAR(30) PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_vim_company FOREIGN KEY (company_code) REFERENCES companies(code)
);
CREATE INDEX IF NOT EXISTS idx_vim_company ON vehicle_imei_map (company_code);
CREATE INDEX IF NOT EXISTS idx_vim_vehicle ON vehicle_imei_map (vehicle_id);

-- ============================================================================
-- End of MASTER 008 (postgres)
-- ============================================================================