-- ============================================================================
-- Migration: MASTER 022 — protocol on the IMEI allowlist (B11, Module 1c)
-- ============================================================================
-- The anti-spoofing allowlist (`tm_vehicle_imei_map`, FR-1.4) is the single
-- authority resolving an IMEI to a tenant + vehicle. With universal brand
-- support the ingestion tier also needs to know WHICH decoder/listener the
-- device belongs to, so the protocol travels with the allowlist entry.
--
-- Additive + idempotent: an existing IMEI keeps working with an empty protocol
-- (the listener port it connected on remains the fallback discriminator).
-- ============================================================================

ALTER TABLE tm_vehicle_imei_map
    ADD COLUMN IF NOT EXISTS protocol VARCHAR(40);

CREATE INDEX IF NOT EXISTS idx_tm_vehicle_imei_map_protocol
    ON tm_vehicle_imei_map (protocol);
