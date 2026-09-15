-- ============================================================================
-- Migration: COMPANY 004 — seed tm_role_menu_access defaults (PRD §6.2)
-- ============================================================================
-- Default per-role menu matrix applied to EVERY tenant (auto-provisioning and
-- the dev tenants use the same rule). Admin CRUD to change it is B12; until then
-- this seed is the source of truth for GET /api/v1/access/menu.
--
--   Admin    : all menus, full rights
--   Manager  : all except the Administrasi module, view/create/edit
--   Operator : operational modules only, view-only
--   Driver   : Utama (dashboard/tracking/trips) only, view-only
--
-- The seed needs the menu catalogue from master. The master schema name is fixed
-- by convention (PRD §6): `adatrack_gps_master`. Instead of interpolating the
-- dynamic name into the whole statement (LIKE patterns contain `%`, which would
-- clash with format()), a TEMP VIEW exposes the catalogue and the INSERT below
-- stays fully static. The catalogue is SKIPPED (notice, not error) when absent —
-- provisioning must never fail because of a missing reference catalogue.

DROP VIEW IF EXISTS _tmp_master_menus;

DO $$
DECLARE
    master_schema TEXT := 'adatrack_gps_master';
BEGIN
    IF to_regclass(master_schema || '.tm_menus') IS NULL THEN
        RAISE NOTICE 'tm_role_menu_access seed skipped: %.tm_menus not found (empty catalogue)', master_schema;
        -- Empty catalogue: the static INSERT below then inserts nothing.
        CREATE TEMP VIEW _tmp_master_menus AS
            SELECT NULL::bigint AS id, NULL::varchar AS code WHERE FALSE;
        RETURN;
    END IF;

    EXECUTE format(
        'CREATE TEMP VIEW _tmp_master_menus AS SELECT id, code FROM %I.tm_menus WHERE enabled = TRUE AND deleted_at IS NULL',
        master_schema);
END $$;

INSERT INTO tm_role_menu_access (role, menu_id, can_view, can_create, can_edit, can_delete, enabled)
SELECT x.role, m.id, x.can_view, x.can_create, x.can_edit, x.can_delete, TRUE
FROM (VALUES
    ('Admin',    '%',                        TRUE, TRUE,  TRUE,  TRUE),
    ('Manager',  'business.main%',           TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.master-data%',    TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.access%',         TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.asset%',          TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.safety%',         TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.analysis%',       TRUE, TRUE,  TRUE,  FALSE),
    ('Manager',  'business.industry%',       TRUE, TRUE,  TRUE,  FALSE),
    ('Operator', 'business.main%',           TRUE, FALSE, FALSE, FALSE),
    ('Operator', 'business.master-data%',    TRUE, FALSE, FALSE, FALSE),
    ('Operator', 'business.asset%',          TRUE, FALSE, FALSE, FALSE),
    ('Operator', 'business.safety%',         TRUE, FALSE, FALSE, FALSE),
    ('Operator', 'business.industry%',       TRUE, FALSE, FALSE, FALSE),
    ('Driver',   'business.main.home%',      TRUE, FALSE, FALSE, FALSE),
    ('Driver',   'business.main.tracking%',  TRUE, FALSE, FALSE, FALSE),
    ('Driver',   'business.main.trips%',     TRUE, FALSE, FALSE, FALSE),
    ('Driver',   'business.tracking%',       TRUE, FALSE, FALSE, FALSE)
) AS x(role, code_pattern, can_view, can_create, can_edit, can_delete)
JOIN _tmp_master_menus m ON m.code LIKE x.code_pattern
ON CONFLICT (role, menu_id) DO UPDATE SET
    can_view = EXCLUDED.can_view,
    can_create = EXCLUDED.can_create,
    can_edit = EXCLUDED.can_edit,
    can_delete = EXCLUDED.can_delete,
    enabled = EXCLUDED.enabled,
    updated_at = CURRENT_TIMESTAMP;

DROP VIEW IF EXISTS _tmp_master_menus;