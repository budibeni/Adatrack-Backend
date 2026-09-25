-- ============================================================================
-- Migration: COMPANY 027 — Enterprise & industry modules (B12, PRD §5.10)
-- ============================================================================
-- Additive-only schema for the Enterprise menus of docs/FRONTEND.md that did not
-- exist yet. Every table follows the house rules:
--   * prefix `tm_` (master/reference) / `th_` / `td_`,
--   * soft delete columns (deleted_at/by/reason) + audit columns (created_by/
--     updated_by/created_at/updated_at) — PRD §6.0.1/§9.4,
--   * `company_code` scoping so a tenant can never observe another tenant,
--   * idempotent (CREATE TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS).
--
-- Part 1/2: master-data, access and safety modules (§1.2 Drivers/Groups, §1.3
-- Access, §1.4 Assets, §1.5 Safety).
-- ============================================================================

-- ---------------------------------------------------------------------------
-- §1.2 Drivers (Pengemudi)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_drivers (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    user_id BIGINT,
    name VARCHAR(120) NOT NULL,
    employee_code VARCHAR(40),
    license_number VARCHAR(60),
    license_type VARCHAR(40),
    license_expiry DATE,
    phone VARCHAR(32),
    email VARCHAR(255),
    address TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'inactive', 'suspended')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_drivers_company ON tm_drivers (company_code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_drivers_status ON tm_drivers (company_code, status) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_drivers_employee_code
    ON tm_drivers (company_code, employee_code) WHERE deleted_at IS NULL AND employee_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tm_drivers_deleted ON tm_drivers (deleted_at);

-- ---------------------------------------------------------------------------
-- §1.2 Groups (Grup) + membership mapping (vehicle/driver)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_groups (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    name VARCHAR(120) NOT NULL,
    group_type VARCHAR(16) NOT NULL DEFAULT 'vehicle' CHECK (group_type IN ('vehicle', 'driver', 'mixed')),
    description TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_groups_company ON tm_groups (company_code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_groups_name
    ON tm_groups (company_code, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_groups_deleted ON tm_groups (deleted_at);

CREATE TABLE IF NOT EXISTS tm_group_members (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    group_id BIGINT NOT NULL,
    member_type VARCHAR(16) NOT NULL CHECK (member_type IN ('vehicle', 'driver')),
    member_id BIGINT NOT NULL,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tm_group_members_group FOREIGN KEY (group_id)
        REFERENCES tm_groups (id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_group_members
    ON tm_group_members (group_id, member_type, member_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_group_members_group ON tm_group_members (group_id) WHERE deleted_at IS NULL;


-- ---------------------------------------------------------------------------
-- §1.3 Access (Personel, Kartu RFID, Log akses)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_personnel (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    user_id BIGINT,
    org_id BIGINT,
    name VARCHAR(120) NOT NULL,
    position VARCHAR(80),
    department VARCHAR(80),
    phone VARCHAR(32),
    email VARCHAR(255),
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_personnel_company ON tm_personnel (company_code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_personnel_deleted ON tm_personnel (deleted_at);

CREATE TABLE IF NOT EXISTS tm_cards (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    card_number VARCHAR(64) NOT NULL,
    card_type VARCHAR(16) NOT NULL DEFAULT 'rfid' CHECK (card_type IN ('rfid', 'nfc', 'mifare', 'other')),
    personnel_id BIGINT,
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'blocked', 'expired')),
    issued_at DATE,
    expires_at DATE,
    notes TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_cards_number
    ON tm_cards (company_code, card_number) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_cards_personnel ON tm_cards (personnel_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_cards_deleted ON tm_cards (deleted_at);

-- Access log is an append-only event stream: no update/delete endpoint (§6.0.1
-- applies to master data; an immutable log is stronger than a soft delete).
CREATE TABLE IF NOT EXISTS tm_access_logs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    personnel_id BIGINT,
    card_id BIGINT,
    vehicle_id BIGINT,
    gate VARCHAR(80),
    direction VARCHAR(8) NOT NULL CHECK (direction IN ('in', 'out')),
    result VARCHAR(16) NOT NULL DEFAULT 'granted' CHECK (result IN ('granted', 'denied')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    notes TEXT,
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_access_logs_time ON tm_access_logs (company_code, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_tm_access_logs_personnel ON tm_access_logs (personnel_id, occurred_at DESC);

-- ---------------------------------------------------------------------------
-- §1.4 Assets (Aset) — the maintenance reminder engine itself stays in B8
-- (tm_maintenance_schedules); this is the asset registry it references.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_assets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    name VARCHAR(160) NOT NULL,
    asset_type VARCHAR(40),
    serial_number VARCHAR(80),
    assigned_vehicle_id BIGINT,
    location VARCHAR(255),
    purchase_date DATE,
    purchase_value NUMERIC(16, 2) CHECK (purchase_value IS NULL OR purchase_value >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'IDR',
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'maintenance', 'retired')),
    notes TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_assets_company ON tm_assets (company_code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_assets_serial
    ON tm_assets (company_code, serial_number) WHERE deleted_at IS NULL AND serial_number IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tm_assets_vehicle ON tm_assets (assigned_vehicle_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_assets_deleted ON tm_assets (deleted_at);

-- ---------------------------------------------------------------------------
-- §1.5 Safety (Incidents) — the safety SCORE is derived from B8 driver
-- behaviour (td_driver_events / th_driver_scores) and never duplicated here.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_incidents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT,
    driver_id BIGINT,
    incident_type VARCHAR(32) NOT NULL CHECK (incident_type IN
        ('overspeed', 'harsh_brake', 'harsh_accel', 'harsh_corner', 'crash', 'sos', 'other')),
    severity VARCHAR(12) NOT NULL DEFAULT 'medium' CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    lat DECIMAL(10, 8),
    lon DECIMAL(11, 8),
    speed DOUBLE PRECISION,
    description TEXT,
    status VARCHAR(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'investigating', 'resolved', 'dismissed')),
    resolved_at TIMESTAMPTZ,
    resolved_by BIGINT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tm_incidents_time ON tm_incidents (company_code, occurred_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_incidents_status ON tm_incidents (company_code, status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_incidents_vehicle ON tm_incidents (vehicle_id, occurred_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_incidents_deleted ON tm_incidents (deleted_at);

-- ============================================================================
-- Part 2/2 — §1.7 administration (Organization, Integrations), §1.1 share
-- lokasi publik (FR-9.3) and the §1.1 heatmap aggregation cache.
-- ============================================================================

-- ---------------------------------------------------------------------------
-- §1.8 Organization (hierarki struktur perusahaan)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_organizations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    parent_id BIGINT,
    code VARCHAR(40),
    name VARCHAR(160) NOT NULL,
    manager_name VARCHAR(120),
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_tm_organizations_parent FOREIGN KEY (parent_id)
        REFERENCES tm_organizations (id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_tm_organizations_company ON tm_organizations (company_code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_organizations_parent ON tm_organizations (parent_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_organizations_name
    ON tm_organizations (company_code, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_organizations_deleted ON tm_organizations (deleted_at);

-- ---------------------------------------------------------------------------
-- §1.8 Integrations (API key + webhook outbound)
-- The secret is NEVER stored in clear text: only a SHA-256 hash plus a display
-- prefix (API keys) or an HMAC signing secret hash (webhooks) is persisted.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_integrations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    name VARCHAR(120) NOT NULL,
    kind VARCHAR(16) NOT NULL CHECK (kind IN ('api_key', 'webhook')),
    endpoint_url VARCHAR(512),
    key_prefix VARCHAR(16),
    secret_hash VARCHAR(128),
    events TEXT[],
    status VARCHAR(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    last_used_at TIMESTAMPTZ,
    notes TEXT,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT ck_tm_integrations_target CHECK (
        (kind = 'api_key' AND endpoint_url IS NULL) OR
        (kind = 'webhook' AND endpoint_url IS NOT NULL)
    )
);
CREATE INDEX IF NOT EXISTS idx_tm_integrations_company ON tm_integrations (company_code) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_integrations_name
    ON tm_integrations (company_code, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_integrations_deleted ON tm_integrations (deleted_at);

-- ---------------------------------------------------------------------------
-- NOTE: public share links (FR-9.3) live in the MASTER schema
-- (`tm_share_links`, migration master 024) because the unauthenticated endpoint
-- must resolve a token WITHOUT knowing the tenant schema (the token is globally
-- unique and carries its company_code).
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- §1.1 Heatmap: cached density cells rebuilt from telemetry history by
-- POST /api/v1/heatmap/rebuild (never computed on every dashboard poll).
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS tm_heatmap_cells (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    cell_lat DECIMAL(10, 6) NOT NULL,
    cell_lon DECIMAL(11, 6) NOT NULL,
    vehicle_id BIGINT,
    sample_count BIGINT NOT NULL DEFAULT 0,
    first_seen_at TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_heatmap_cells
    ON tm_heatmap_cells (company_code, cell_lat, cell_lon, COALESCE(vehicle_id, 0));
CREATE INDEX IF NOT EXISTS idx_tm_heatmap_cells_company ON tm_heatmap_cells (company_code, sample_count DESC);
