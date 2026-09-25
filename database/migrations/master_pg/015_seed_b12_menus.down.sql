-- Do nothing on down for seed data, or delete the seeded data
SET search_path TO adatrack_gps_master;
DELETE FROM tm_menus;
DELETE FROM tm_modules;
