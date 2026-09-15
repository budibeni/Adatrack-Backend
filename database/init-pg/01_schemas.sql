-- ============================================================================
-- PostgreSQL bootstrap 01 — schemas + privileges (PRD §6, §7.1)
-- ============================================================================
-- ONE physical database (POSTGRES_DB, default `adatrack_gps_db`) hosts every
-- tenant as a SCHEMA; the tenant is selected via search_path in the pgx DSN
-- (internal/tenant):
--
--   adatrack_gps_master   → master schema (auth + IMEI map + reference wilayah)
--   adatrack_gps_default  → platform tenant (PRD §6.1 "platform tenant")
--   adatrack_gps_dev001   → dev/staging sample tenant
--
-- Idempotent: safe to re-run (`docker compose` entrypoint and scripts/migrate.sh
-- both execute this file).
-- ============================================================================

CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
CREATE SCHEMA IF NOT EXISTS adatrack_gps_default;
CREATE SCHEMA IF NOT EXISTS adatrack_gps_dev001;

-- The application role (POSTGRES_USER) must own/alter objects in every schema so
-- auto-provisioning (CREATE TABLE / CREATE PARTITION) works without a superuser.
GRANT ALL ON SCHEMA adatrack_gps_master  TO CURRENT_USER;
GRANT ALL ON SCHEMA adatrack_gps_default TO CURRENT_USER;
GRANT ALL ON SCHEMA adatrack_gps_dev001  TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_master  TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_default TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_dev001  TO CURRENT_USER;
GRANT ALL ON ALL SEQUENCES IN SCHEMA adatrack_gps_master  TO CURRENT_USER;
GRANT ALL ON ALL SEQUENCES IN SCHEMA adatrack_gps_default TO CURRENT_USER;
GRANT ALL ON ALL SEQUENCES IN SCHEMA adatrack_gps_dev001  TO CURRENT_USER;