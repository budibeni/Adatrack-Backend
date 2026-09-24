-- ============================================================================
-- Migration: COMPANY 020 — tri-state ACC in th_telemetry_logs (B6, PRD FR-2.1)
-- ============================================================================
-- B6 hardening: the ingestion payload carries the ACC line of the DEVICE. Some
-- packets/protocols never report it (fuel-only sentences, LBS frames, Teltonika
-- devices without an ignition IO), and reporting `acc = false` for those frames
-- is an inference the audit asked to remove. `acc_status` therefore becomes
-- NULLABLE so "unknown" is stored as NULL instead of a fabricated 0:
--   * NULL → the device did not report ACC for that record,
--   * 0/1  → the literal device value.
-- Positive columns/indexes are untouched (additive, idempotent). The ALTER on a
-- partitioned parent propagates to every partition.
-- ============================================================================

ALTER TABLE th_telemetry_logs
    ALTER COLUMN acc_status DROP NOT NULL;

COMMENT ON COLUMN th_telemetry_logs.acc_status IS
    'Device ACC line: 1 = ON, 0 = OFF, NULL = the device did not report ACC (B6)';
