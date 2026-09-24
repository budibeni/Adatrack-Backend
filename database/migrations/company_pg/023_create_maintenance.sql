-- ============================================================================
-- Migration: COMPANY 023 — B8 maintenance scheduling (PRD §21.2 row 5, §5.10 1.4)
-- ============================================================================
--   * `tm_maintenance_schedules` — master row per vehicle × maintenance item:
--     the interval (km / engine hours / days) plus the baseline measured at the
--     last service, and the computed next-due values.
--   * `td_maintenance_logs`      — one row per executed service (odometer /
--     engine-hours snapshot, cost, vendor).
--
-- worker-alert evaluates the schedules against `tm_vehicles.odometer_km` /
-- `engine_hours` (B7.1) and the calendar, and raises the `maintenance_due` alert
-- from this phase. CRUD/UI for these tables belongs to the Maintenance module of
-- B12 — B8 owns the *reminder engine*, exactly as PRD §5.10 1.4 describes
-- ("maintenance menyambung B8").
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_maintenance_schedules (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,

    name VARCHAR(120) NOT NULL,
    maintenance_type VARCHAR(24) NOT NULL DEFAULT 'service' CHECK (maintenance_type IN
        ('oil_change', 'tire', 'service', 'inspection', 'brake', 'filter', 'other')),

    -- Intervals: any subset may be set; a NULL interval simply never triggers on
    -- that dimension (the reminder is the OR of the configured dimensions).
    interval_km NUMERIC(12, 3) CHECK (interval_km IS NULL OR interval_km > 0),
    interval_engine_hours NUMERIC(12, 2) CHECK (interval_engine_hours IS NULL OR interval_engine_hours > 0),
    interval_days INT CHECK (interval_days IS NULL OR interval_days > 0),

    -- Baseline captured at the last service.
    last_service_at DATE,
    last_service_odometer_km NUMERIC(12, 3) CHECK (last_service_odometer_km IS NULL OR last_service_odometer_km >= 0),
    last_service_engine_hours NUMERIC(12, 2) CHECK (last_service_engine_hours IS NULL OR last_service_engine_hours >= 0),

    -- Pre-emptive reminder margin (alert fires when due_at - margin is reached).
    reminder_km_before NUMERIC(12, 3) NOT NULL DEFAULT 500 CHECK (reminder_km_before >= 0),
    reminder_days_before INT NOT NULL DEFAULT 7 CHECK (reminder_days_before >= 0),

    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT,

    last_reminder_at TIMESTAMPTZ,
    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tm_maintenance_schedules_vehicle
    ON tm_maintenance_schedules (vehicle_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_maintenance_schedules_company_active
    ON tm_maintenance_schedules (company_code, is_active) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS td_maintenance_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    schedule_id BIGINT,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,

    performed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    odometer_km NUMERIC(12, 3) CHECK (odometer_km IS NULL OR odometer_km >= 0),
    engine_hours NUMERIC(12, 2) CHECK (engine_hours IS NULL OR engine_hours >= 0),
    cost NUMERIC(14, 2) CHECK (cost IS NULL OR cost >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'IDR',
    vendor VARCHAR(160),
    notes TEXT,
    status VARCHAR(12) NOT NULL DEFAULT 'done' CHECK (status IN ('scheduled', 'done', 'cancelled')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_td_maintenance_logs_schedule FOREIGN KEY (schedule_id)
        REFERENCES tm_maintenance_schedules (id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_td_maintenance_logs_vehicle_time
    ON td_maintenance_logs (vehicle_id, performed_at DESC);
CREATE INDEX IF NOT EXISTS idx_td_maintenance_logs_schedule
    ON td_maintenance_logs (schedule_id);
