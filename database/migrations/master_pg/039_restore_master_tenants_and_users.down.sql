-- Revert migration 039
DELETE FROM adatrack_gps_master.tm_users WHERE id IN (15, 17, 24, 33, 38, 39, 42, 141);
DELETE FROM adatrack_gps_master.tm_companies WHERE code IN ('TESST001', 'BETEIN01', 'SIOTKE01');
