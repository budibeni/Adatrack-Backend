-- Revert migration 038
DELETE FROM adatrack_gps_master.tm_menus WHERE code LIKE '%.%';
