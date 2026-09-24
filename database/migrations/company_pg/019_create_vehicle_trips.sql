-- ============================================================================
-- Migration: COMPANY 019 — th_vehicle_trips + td_vehicle_stops (B7.2, PRD FR-2.6)
-- ============================================================================
-- Trip segmentation of the B7.2 state machine (worker-live):
--   * `th_vehicle_trips`  — Transaksi Header (§6.0): one row per trip with
--                           start/end time + position, distance, max/avg speed,
--                           stop_count and duration.
--   * `td_vehicle_stops`  — Transaksi Detail (§6.0): the stops belonging to a
--                           trip (FK → th_vehicle_trips.id, ON DELETE CASCADE).
--
-- Soft delete columns follow §6.0.1 (master/transaction-header tables); the FK
-- therefore uses ON DELETE CASCADE so the retention/hard-delete job can purge a
-- trip together with its stops.
-- ============================================================================

CREATE TABLE IF NOT EXISTS th_vehicle_trips (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,
    imei VARCHAR(30) NOT NULL,

    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    start_lat DECIMAL(10, 8),
    start_lon DECIMAL(11, 8),
    end_lat DECIMAL(10, 8),
    end_lon DECIMAL(11, 8),

    distance_km NUMERIC(12, 3) NOT NULL DEFAULT 0,
    max_speed_kmh NUMERIC(7, 2) NOT NULL DEFAULT 0,
    avg_speed_kmh NUMERIC(7, 2) NOT NULL DEFAULT 0,
    stop_count INT NOT NULL DEFAULT 0,
    duration_seconds INT NOT NULL DEFAULT 0,

    created_by BIGINT,
    updated_by BIGINT,
    deleted_at TIMESTAMPTZ,
    deleted_by BIGINT,
    delete_reason VARCHAR(255),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT th_vehicle_trips_window_check CHECK (end_time >= start_time),
    CONSTRAINT th_vehicle_trips_distance_check CHECK (distance_km >= 0),
    CONSTRAINT th_vehicle_trips_duration_check CHECK (duration_seconds >= 0)
);

CREATE INDEX IF NOT EXISTS idx_th_vehicle_trips_vehicle_time
    ON th_vehicle_trips (vehicle_id, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_th_vehicle_trips_company_time
    ON th_vehicle_trips (company_code, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_th_vehicle_trips_deleted
    ON th_vehicle_trips (deleted_at);

CREATE TABLE IF NOT EXISTS td_vehicle_stops (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trip_id BIGINT NOT NULL,
    company_code VARCHAR(20) NOT NULL,
    vehicle_id BIGINT NOT NULL,

    start_time TIMESTAMPTZ NOT NULL,
    end_time TIMESTAMPTZ NOT NULL,
    duration_seconds INT NOT NULL DEFAULT 0,
    lat DECIMAL(10, 8),
    lon DECIMAL(11, 8),

    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,

    CONSTRAINT fk_td_vehicle_stops_trip FOREIGN KEY (trip_id)
        REFERENCES th_vehicle_trips (id) ON DELETE CASCADE,
    CONSTRAINT td_vehicle_stops_window_check CHECK (end_time >= start_time),
    CONSTRAINT td_vehicle_stops_duration_check CHECK (duration_seconds >= 0)
);

CREATE INDEX IF NOT EXISTS idx_td_vehicle_stops_trip ON td_vehicle_stops (trip_id);
CREATE INDEX IF NOT EXISTS idx_td_vehicle_stops_vehicle_time
    ON td_vehicle_stops (vehicle_id, start_time DESC);
