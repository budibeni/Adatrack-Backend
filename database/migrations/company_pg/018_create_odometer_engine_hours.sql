-- ============================================================================
-- Migration: COMPANY 018 — odometer & engine hours (B7.1, PRD FR-2.5, §6.2)
-- ============================================================================
-- Additive-only (§1 global rules): `tm_vehicles` gains the two cumulative
-- counters of the fleet-management core. worker-live accumulates them from the
-- telemetry stream and flushes deltas (never a computed total), therefore:
--   * DEFAULT 0        → existing rows keep a valid, non-null counter;
--   * CHECK >= 0       → anti-rollback: the odometer can never decrease;
--   * odometer_updated_at → observability (last successful flush per vehicle).
--
-- `ALTER TABLE ... IF NOT EXISTS` + a guarded constraint make the migration
-- idempotent (safe to re-run, B7.1 acceptance).
-- ============================================================================

ALTER TABLE tm_vehicles
    ADD COLUMN IF NOT EXISTS odometer_km NUMERIC(12, 3) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS engine_hours NUMERIC(12, 3) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS odometer_updated_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'tm_vehicles_metering_nonnegative'
    ) THEN
        ALTER TABLE tm_vehicles
            ADD CONSTRAINT tm_vehicles_metering_nonnegative
            CHECK (odometer_km >= 0 AND engine_hours >= 0);
    END IF;
END $$;

-- Hot path of the B7.1 flush (UPDATE ... WHERE id = $1) plus ops reporting.
CREATE INDEX IF NOT EXISTS idx_tm_vehicles_odometer_updated
    ON tm_vehicles (odometer_updated_at DESC);
