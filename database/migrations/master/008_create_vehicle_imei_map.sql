-- ============================================================================
-- Migration: MASTER 007 — vehicle_imei_map
-- ============================================================================
-- Master lookup: IMEI → company_code. Dipakai ingestion-tcp untuk tenant
-- resolution (anti-spoofing) sebelum publish ke NATS (PRD §6.1 / Key Decision 3).
-- Diperbarui otomatis saat vehicle didaftarkan di company DB.
-- Besar: FK ke companies.code (tanpa FK cross-DB ke vehicle di company DB).
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicle_imei_map (
    imei VARCHAR(30) PRIMARY KEY,                   -- device IMEI
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT,                              -- vehicle.id di company DB (denormalisasi, tanpa FK cross-DB)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (company_code) REFERENCES companies(code),
    INDEX idx_company (company_code),
    INDEX idx_vehicle (vehicle_id)
) ENGINE=InnoDB DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of MASTER 007
-- ============================================================================