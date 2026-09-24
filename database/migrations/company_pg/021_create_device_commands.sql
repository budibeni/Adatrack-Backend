-- ============================================================================
-- Migration: COMPANY 021 — td_device_commands (B8, PRD §21.2 row 1)
-- ============================================================================
-- Downlink / remote command audit trail (PRD §9.4):
--   * the REST API inserts a `pending` row and publishes
--     `command.request.<company>`;
--   * ingestion-tcp writes the frame to the live device socket → `sent`
--     (or `offline` when the IMEI has no connection, `failed` when the write
--     failed / the protocol has no encoder);
--   * the device's online-command reply (GT06 0x21/0x15) flips the row to
--     `acked` and stores the raw reply in `detail`/`ack_content`;
--   * a reply that never arrives inside ACK_TIMEOUT_SECONDS becomes `timeout`.
--
-- `deleted_at` follows §6.0.1 (soft delete for transactional tables).
-- `parameters` keeps command-specific arguments (e.g. interval_seconds) so the
-- audit row is self-contained.
-- ============================================================================

CREATE TABLE IF NOT EXISTS td_device_commands (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id VARCHAR(64) NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,

    command VARCHAR(32) NOT NULL,
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    detail TEXT,
    ack_content TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT td_device_commands_command_check CHECK (
        command IN ('engine_cut', 'engine_restore', 'set_interval', 'reboot', 'locate')),
    CONSTRAINT td_device_commands_status_check CHECK (
        status IN ('pending', 'sent', 'acked', 'offline', 'failed', 'timeout'))
);

-- One row per request: the API generates the request_id and a retry must not
-- create a second audit row.
CREATE UNIQUE INDEX IF NOT EXISTS uq_td_device_commands_request_id
    ON td_device_commands (request_id);
CREATE INDEX IF NOT EXISTS idx_td_device_commands_vehicle_time
    ON td_device_commands (vehicle_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_td_device_commands_company_status
    ON td_device_commands (company_code, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_td_device_commands_imei_pending
    ON td_device_commands (imei, created_at DESC)
    WHERE status IN ('pending', 'sent');
CREATE INDEX IF NOT EXISTS idx_td_device_commands_deleted
    ON td_device_commands (deleted_at);
