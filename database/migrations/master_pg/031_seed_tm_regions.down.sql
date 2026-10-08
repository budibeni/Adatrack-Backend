SET search_path TO adatrack_gps_master, public;

DELETE FROM tm_regions WHERE id IN (1,2,3,4,5,101,102,103,104,105,106);
