-- ============================================================================
-- PostgreSQL init: CREATE tenant schemas + privileges (docker compose only;
-- pada server produksi ini cukup dijalankan sekali sebagai superuser).
--
-- Arsitektur provider postgres (PRD §6/§7): SATU physical database
-- (POSTGRES_DB, dev: adatrack_gps_db) menampung semua "database" tenant sebagai
-- SCHEMAS. Per-tenant dipilih lewat parameter search_path pada DSN pgx
-- (internal/tenant — MasterDSN/DBDSN):
--     postgres://user@host/adatrack_gps_db?sslmode=...&search_path=adatrack_gps_xxx
--
-- Skema yang dibuat:
--   adatrack_gps_master        = database master (auth + vehicle_imei_map + referensi)
--   adatrack_gps_default       = tenant 'default' (dev)
--   adatrack_gps_dev001        = tenant contoh 'dev001' (dev)
-- ============================================================================

CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
CREATE SCHEMA IF NOT EXISTS adatrack_gps_default;
CREATE SCHEMA IF NOT EXISTS adatrack_gps_dev001;

-- user aplikasi (POSTGRES_USER) diberi hak schema agar auto-provision
-- (CREATE TABLE dll) berjalan. Di docker compose postgres user tsb adalah
-- owner POSTGRES_DB sehingga sudah punya hak; pernyataan berikut adalah
-- pengaman untuk skenario user terpisah.
GRANT ALL ON SCHEMA adatrack_gps_master    TO CURRENT_USER;
GRANT ALL ON SCHEMA adatrack_gps_default   TO CURRENT_USER;
GRANT ALL ON SCHEMA adatrack_gps_dev001    TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_master  TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_default TO CURRENT_USER;
GRANT ALL ON ALL TABLES IN SCHEMA adatrack_gps_dev001  TO CURRENT_USER;