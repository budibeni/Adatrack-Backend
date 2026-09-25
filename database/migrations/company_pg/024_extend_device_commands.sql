-- ============================================================================
-- Migration: COMPANY 024 — downlink command kinds (B8, PRD §21.2 row 1)
-- ============================================================================
-- The TK103 matrix (upstream Tk103ProtocolEncoder) adds two parameterless
-- commands to the closed whitelist of `td_device_commands.command`:
--   * `device_version` — `(<IMEI>AP07)` firmware/hardware version request;
--   * `position_stop`  — `(<IMEI>AR0000000000)` stop periodic reporting.
--
-- The destructive upstream command `AX01` (reset odometer) stays deliberately
-- unexposed: it would corrupt the B7.1 odometer history.
--
-- DROP CONSTRAINT IF EXISTS + ADD keeps the migration idempotent and safe for
-- schemas created before this file existed (same pattern as migration 022 did for
-- th_alerts.type).
-- ============================================================================

ALTER TABLE td_device_commands DROP CONSTRAINT IF EXISTS td_device_commands_command_check;
ALTER TABLE td_device_commands ADD CONSTRAINT td_device_commands_command_check CHECK (
    command IN ('engine_cut', 'engine_restore', 'set_interval', 'reboot', 'locate',
                'device_version', 'position_stop'));
