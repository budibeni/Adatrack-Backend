CREATE SCHEMA IF NOT EXISTS adatrack_gps_master;
SET search_path TO adatrack_gps_master;
CREATE TABLE IF NOT EXISTS tm_schema_migrations (
    version VARCHAR(50) PRIMARY KEY,
    checksum VARCHAR(100) NOT NULL,
    applied_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    success BOOLEAN NOT NULL,
    duration_ms INT,
    applied_by VARCHAR(100)
);
