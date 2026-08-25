-- ============================================================================
-- Migration: COMPANY 002 — vehicles (Enterprise-standard schema)
-- ============================================================================
-- Skema kendaraan mengikuti enterprise adatrack-management standard.
-- Field enterprise: make, model, VIN (chassis_number), engine_number,
-- fuel_type, vehicle_category_code, vehicle_type_code, registration & compliance,
-- physical specs, driver_user_id, device info, live-state denorm, soft-delete.
--
-- vehicle_type VARCHAR(50) & driver_name VARCHAR(100) dipertahankan untuk
-- backward-compat, ditandai DEPRECATED. Pakai vehicle_type_code & driver_user_id
-- di kode baru.
--
-- vehicle_category_code & vehicle_type_code = DENORMALISASI code dari
-- master.vehicle_categories / master.vehicle_types (tanpa FK cross-DB),
-- konsisten pola vehicle_imei_map & user_company_access.
--
-- NOTE: urutan migrasi = user_company_access (001) -> vehicles (002) ->
--       user_vehicles (003). driver_user_id = denormalisasi user_id
--       references user_company_access.user_id (NO cross-DB FK constraint).
-- ============================================================================

CREATE TABLE IF NOT EXISTS vehicles (
    -- Core identity
    id           BIGINT AUTO_INCREMENT PRIMARY KEY,
    imei         VARCHAR(30) UNIQUE NOT NULL,
    plate_number VARCHAR(20) NOT NULL,

    -- Vehicle Identification (enterprise-standard)
    make                 VARCHAR(100) NULL DEFAULT NULL,
    model                VARCHAR(100) NULL DEFAULT NULL,
    variant              VARCHAR(100) NULL DEFAULT NULL,
    year_of_manufacture  YEAR NULL DEFAULT NULL,
    engine_number        VARCHAR(100) NULL DEFAULT NULL,
    chassis_number       VARCHAR(100) NULL DEFAULT NULL COMMENT 'VIN — unique per vehicle',
    color                VARCHAR(50)  NULL DEFAULT NULL,
    fuel_type            ENUM('petrol','diesel','electric','hybrid','CNG','LPG','hydrogen') NULL DEFAULT NULL,

    -- Vehicle Classification (reference master.vehicle_categories / vehicle_types)
    vehicle_category_code VARCHAR(20) NULL DEFAULT NULL,
    vehicle_type_code     VARCHAR(30) NULL DEFAULT NULL,

    -- Legacy (DEPRECATED — pakai vehicle_type_code di kode baru)
    vehicle_type VARCHAR(50) NULL DEFAULT NULL,
    driver_name  VARCHAR(100) NULL DEFAULT NULL,

    -- Registration & Compliance
    registration_number   VARCHAR(50)  NULL DEFAULT NULL,
    registration_expiry   DATE         NULL DEFAULT NULL,
    insurance_number      VARCHAR(100) NULL DEFAULT NULL,
    insurance_expiry      DATE         NULL DEFAULT NULL,
    road_tax_expiry       DATE         NULL DEFAULT NULL,
    inspection_expiry     DATE         NULL DEFAULT NULL,

    -- Physical Specifications (semua dalam mm kecuali yang dinyatakan)
    gross_vehicle_weight  DECIMAL(10,2) NULL DEFAULT NULL,
    payload_capacity      DECIMAL(10,2) NULL DEFAULT NULL,
    vehicle_length        DECIMAL(6,2)  NULL DEFAULT NULL,
    vehicle_width         DECIMAL(6,2)  NULL DEFAULT NULL,
    vehicle_height        DECIMAL(6,2)  NULL DEFAULT NULL,
    wheelbase             DECIMAL(6,2)  NULL DEFAULT NULL,

    -- Driver (modern FK-based — references user_company_access.user_id)
    driver_user_id   BIGINT NULL DEFAULT NULL,

    -- GPS Device Info
    device_model        VARCHAR(100) NULL DEFAULT NULL,
    firmware_version    VARCHAR(50)  NULL DEFAULT NULL,

    -- Live State (denormalisasi dari Redis)
    last_seen_at         TIMESTAMP     NULL DEFAULT NULL,
    current_latitude     DECIMAL(10,8) NULL DEFAULT NULL,
    current_longitude    DECIMAL(11,8) NULL DEFAULT NULL,
    current_speed        DECIMAL(5,2)  NULL DEFAULT NULL,

    -- Metadata & Soft Delete
    notes        TEXT NULL,
    status       ENUM('active', 'inactive', 'maintenance') DEFAULT 'active',
    deleted_at   TIMESTAMP NULL DEFAULT NULL,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    -- Constraints & Indexes
    CONSTRAINT uq_vehicles_chassis_number UNIQUE (chassis_number),
    INDEX idx_imei                   (imei),
    INDEX idx_status                 (status),
    INDEX idx_vehicle_type_code      (vehicle_type_code),
    INDEX idx_vehicle_category_code  (vehicle_category_code),
    INDEX idx_driver_user_id         (driver_user_id),
    INDEX idx_last_seen_at           (last_seen_at),
    INDEX idx_deleted_at             (deleted_at),
    INDEX idx_make_model             (make, model),
    INDEX idx_current_location       (current_latitude, current_longitude)
) ENGINE=InnoDB
DEFAULT CHARACTER SET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- ============================================================================
-- End of COMPANY 002
-- ============================================================================