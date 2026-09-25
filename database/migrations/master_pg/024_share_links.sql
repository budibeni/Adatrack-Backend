-- ============================================================================
-- Migration: MASTER 024 — tm_share_links (public location share, FR-9.3 / B12)
-- ============================================================================
-- A share link exposes the last known position of an explicit vehicle set to an
-- UNAUTHENTICATED page, so the token must be resolvable without knowing the
-- tenant schema. It therefore lives in the master schema (globally unique token,
-- carrying its company_code) and is governed by TTL + explicit revocation.
--
-- Access model: create/list/revoke need Admin in the owning tenant; the public
-- read endpoint is `GET /api/v1/share/{token}` and only ever returns position
-- data for the vehicles of that single company.
-- ============================================================================

CREATE TABLE IF NOT EXISTS tm_share_links (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    token VARCHAR(64) NOT NULL,
    label VARCHAR(120),
    scope VARCHAR(16) NOT NULL DEFAULT 'vehicles' CHECK (scope IN ('vehicles', 'fleet')),
    vehicle_ids BIGINT[],
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    view_count BIGINT NOT NULL DEFAULT 0,
    last_viewed_at TIMESTAMPTZ,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tm_share_links_token ON tm_share_links (token);
CREATE INDEX IF NOT EXISTS idx_tm_share_links_company ON tm_share_links (company_code) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tm_share_links_expiry ON tm_share_links (expires_at) WHERE revoked_at IS NULL;
