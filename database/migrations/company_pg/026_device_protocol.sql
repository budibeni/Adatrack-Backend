-- ============================================================================
-- Migration: COMPANY 026 — device protocol/brand on tm_vehicles (B11, Module 1c)
-- ============================================================================
-- Universal brand support: a vehicle row records which protocol family its
-- tracker speaks, validated against `internal/protocol` (and master
-- `tm_protocols`) before the device is registered. The value is copied into
-- master `tm_vehicle_imei_map` so the ingestion tier can route frames to the
-- right decoder.
--
-- Additive + idempotent; empty string / NULL means "unknown, resolve by
-- listener port" so every pre-existing row keeps working.
-- ============================================================================

ALTER TABLE tm_vehicles
    ADD COLUMN IF NOT EXISTS protocol VARCHAR(40),
    ADD COLUMN IF NOT EXISTS protocol_port INT,
    ADD COLUMN IF NOT EXISTS brand VARCHAR(80);

CREATE INDEX IF NOT EXISTS idx_tm_vehicles_protocol ON tm_vehicles (protocol);
