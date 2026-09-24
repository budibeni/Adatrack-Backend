package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// openPG opens a pool bound to one schema (search_path).
func openPG(cfg pgConfig, schema string) (*sql.DB, error) {
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&search_path=%s",
		cfg.user, cfg.password, cfg.host, cfg.port, cfg.db, schema)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// vehicleIDForIMEI resolves the tenant + vehicle of a registered device.
func vehicleIDForIMEI(ctx context.Context, master *sql.DB, imei string) (int64, string, error) {
	var (
		id      int64
		company string
	)
	err := master.QueryRowContext(ctx, `
SELECT COALESCE(vehicle_id, 0), company_code
FROM tm_vehicle_imei_map
WHERE imei = $1 AND deleted_at IS NULL`, imei).Scan(&id, &company)
	if err != nil {
		return 0, "", fmt.Errorf("resolve IMEI %s: %w", imei, err)
	}
	return id, strings.ToUpper(company), nil
}

// metering is the pair of B7.1 counters of one vehicle.
type metering struct {
	odometerKM  float64
	engineHours float64
}

// readMetering reads the odometer/engine-hour counters (B7.1).
func readMetering(ctx context.Context, tenant *sql.DB, vehicleID int64) (metering, error) {
	var m metering
	err := tenant.QueryRowContext(ctx, `
SELECT odometer_km::float8, engine_hours::float8 FROM tm_vehicles WHERE id = $1`,
		vehicleID).Scan(&m.odometerKM, &m.engineHours)
	if err != nil {
		return metering{}, fmt.Errorf("read metering: %w", err)
	}
	return m, nil
}

// restoreMetering writes the counters back (cleanup: the E2E fixture must be
// repeatable, and the plan asserts DELTAS so a leftover increment would only
// pollute the dataset).
func restoreMetering(ctx context.Context, tenant *sql.DB, vehicleID int64, m metering) error {
	_, err := tenant.ExecContext(ctx, `
UPDATE tm_vehicles SET odometer_km = $2, engine_hours = $3 WHERE id = $1`,
		vehicleID, m.odometerKM, m.engineHours)
	return err
}

// tripRow is one `th_vehicle_trips` row of the run.
type tripRow struct {
	id              int64
	startTime       time.Time
	endTime         time.Time
	distanceKM      float64
	maxSpeedKMH     float64
	avgSpeedKMH     float64
	stopCount       int
	durationSeconds int
}

// tripsSince lists the trips of a vehicle started after `since`.
func tripsSince(ctx context.Context, tenant *sql.DB, vehicleID int64, since time.Time) ([]tripRow, error) {
	rows, err := tenant.QueryContext(ctx, `
SELECT id, start_time, end_time, distance_km::float8, max_speed_kmh::float8,
       avg_speed_kmh::float8, stop_count, duration_seconds
  FROM th_vehicle_trips
 WHERE vehicle_id = $1 AND start_time >= $2 AND deleted_at IS NULL
 ORDER BY start_time`, vehicleID, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("trips since: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []tripRow
	for rows.Next() {
		var t tripRow
		if err := rows.Scan(&t.id, &t.startTime, &t.endTime, &t.distanceKM,
			&t.maxSpeedKMH, &t.avgSpeedKMH, &t.stopCount, &t.durationSeconds); err != nil {
			return nil, fmt.Errorf("scan trip: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// stopRow is one `td_vehicle_stops` row.
type stopRow struct {
	startTime       time.Time
	endTime         time.Time
	durationSeconds int
	lat             float64
	lon             float64
}

// stopsOfTrip lists the stops belonging to a trip.
func stopsOfTrip(ctx context.Context, tenant *sql.DB, tripID int64) ([]stopRow, error) {
	rows, err := tenant.QueryContext(ctx, `
SELECT start_time, end_time, duration_seconds, lat::float8, lon::float8
  FROM td_vehicle_stops WHERE trip_id = $1 ORDER BY start_time`, tripID)
	if err != nil {
		return nil, fmt.Errorf("stops of trip: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []stopRow
	for rows.Next() {
		var s stopRow
		if err := rows.Scan(&s.startTime, &s.endTime, &s.durationSeconds, &s.lat, &s.lon); err != nil {
			return nil, fmt.Errorf("scan stop: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// telemetryCount counts the `th_telemetry_logs` rows of the run (playback input).
func telemetryCount(ctx context.Context, tenant *sql.DB, vehicleID int64, since time.Time) (int, error) {
	var n int
	err := tenant.QueryRowContext(ctx, `
SELECT count(*) FROM th_telemetry_logs WHERE vehicle_id = $1 AND "timestamp" >= $2`,
		vehicleID, since.UTC()).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("telemetry count: %w", err)
	}
	return n, nil
}

// deleteTrips removes the trips created by the run (stops cascade).
func deleteTrips(ctx context.Context, tenant *sql.DB, tripIDs []int64) error {
	for _, id := range tripIDs {
		if _, err := tenant.ExecContext(ctx, `DELETE FROM th_vehicle_trips WHERE id = $1`, id); err != nil {
			return fmt.Errorf("delete trip %d: %w", id, err)
		}
	}
	return nil
}
