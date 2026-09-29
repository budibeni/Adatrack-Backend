ALTER TABLE adatrack_gps_master.tm_gps_devices ADD COLUMN iccid VARCHAR(50) REFERENCES adatrack_gps_master.tm_sim_cards(iccid) ON DELETE SET NULL;
