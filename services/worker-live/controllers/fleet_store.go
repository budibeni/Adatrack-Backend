package controllers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"adatrack_gps/internal/tenant"
)

// TripEventType labels `trip_events_total{company_code,event_type}` (PRD §10.1).
type TripEventType string

const (
	// TripEventStart is emitted when the FR-2.6 machine opens a trip.
	TripEventStart TripEventType = "trip_start"
	// TripEventEnd is emitted when a trip is closed (and persisted).
	TripEventEnd TripEventType = "trip_end"
)

// TripRecord is one `th_vehicle_trips` row (PRD §6.2).
type TripRecord struct {
	CompanyCode     string
	VehicleID       int64
	IMEI            string
	StartTime       time.Time
	EndTime         time.Time
	StartLat        float64
	StartLon        float64
	EndLat          float64
	EndLon          float64
	DistanceKM      float64
	MaxSpeedKMH     float64
	AvgSpeedKMH     float64
	StopCount       int
	DurationSeconds int
}

// StopRecord is one `td_vehicle_stops` row (PRD §6.2).
type StopRecord struct {
	StartTime       time.Time
	EndTime         time.Time
	Lat             float64
	Lon             float64
	DurationSeconds int
}

// FleetStore persists the B7 accumulators. It is an interface so the accumulator
// and its flush logic are testable without PostgreSQL; production uses
// PostgresFleetStore on top of the shared tenant manager.
type FleetStore interface {
	// AddMetering adds the accumulated DELTAS to `tm_vehicles` (FR-2.5). The
	// statement only ever increases the counters (anti-rollback).
	AddMetering(ctx context.Context, companyCode string, vehicleID int64, odometerKM, engineHours float64) error
	// InsertTrip stores a closed trip and returns its id (FR-2.6).
	InsertTrip(ctx context.Context, companyCode string, trip TripRecord) (int64, error)
	// InsertStops stores the stops of one trip (FR-2.6, `td_vehicle_stops`).
	InsertStops(ctx context.Context, companyCode string, tripID int64, stops []StopRecord) error
	// Readiness reports whether the store can persist (readiness probe).
	Readiness(ctx context.Context) error
}

// PostgresFleetStore writes the fleet accumulators into the tenant schemas
// (`tm_vehicles` + `th_vehicle_trips`/`td_vehicle_stops`, migrations 018/019).
type PostgresFleetStore struct {
	tenants *tenant.Manager
}

// NewPostgresFleetStore wraps the shared tenant manager.
func NewPostgresFleetStore(tenants *tenant.Manager) *PostgresFleetStore {
	return &PostgresFleetStore{tenants: tenants}
}

// Readiness pings the master schema and every tenant pool (PRD §10.2).
func (s *PostgresFleetStore) Readiness(ctx context.Context) error {
	if s == nil || s.tenants == nil {
		return fmt.Errorf("fleet store not initialised")
	}
	if err := s.tenants.Master().Ping(ctx); err != nil {
		return fmt.Errorf("postgres_master: %w", err)
	}
	if err := s.tenants.Health(ctx); err != nil {
		return fmt.Errorf("tenant_pools: %w", err)
	}
	return nil
}

// AddMetering adds the odometer/engine-hour deltas to the vehicle row. The guard
// clauses keep the counters monotonic (FR-2.5 anti-rollback):
//   - non-positive deltas are rejected by the caller AND by the WHERE clause,
//   - soft-deleted vehicles are skipped (no write for a deleted row).
//
// The parameters are cast to `float8` EXPLICITLY (and this is load-bearing): in
// this stack a Go `float64` bound to a parameter whose type PostgreSQL infers as
// `numeric` — which is what `numeric_column + $2` does — is encoded as 0 by pgx,
// silently turning every delta into a no-op. `$2::float8` pins the parameter type
// at parse time and makes the arithmetic exact (verified by
// TestITAddMeteringAccumulates against live PostgreSQL).
func (s *PostgresFleetStore) AddMetering(ctx context.Context, companyCode string, vehicleID int64, odometerKM, engineHours float64) error {
	if s == nil || s.tenants == nil {
		return fmt.Errorf("fleet store not initialised")
	}
	if vehicleID <= 0 || (odometerKM <= 0 && engineHours <= 0) {
		return nil
	}
	pool, err := s.tenants.DB(companyCode)
	if err != nil {
		return err
	}
	res, err := pool.DB.ExecContext(ctx, `
		UPDATE tm_vehicles
		   SET odometer_km = odometer_km + $2::float8,
		       engine_hours = engine_hours + $3::float8,
		       odometer_updated_at = CURRENT_TIMESTAMP,
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $1
		   AND deleted_at IS NULL
		   AND $2::float8 >= 0 AND $3::float8 >= 0`,
		vehicleID, odometerKM, engineHours)
	if err != nil {
		return fmt.Errorf("store: add metering vehicle %d: %w", vehicleID, err)
	}
	if n, aerr := res.RowsAffected(); aerr == nil && n == 0 {
		slog.Debug("worker-live: metering update matched no row (soft-deleted or unknown vehicle?)",
			"company", companyCode, "vehicle_id", vehicleID)
	}
	return nil
}

// InsertTrip stores one closed trip (FR-2.6) and returns its generated id.
func (s *PostgresFleetStore) InsertTrip(ctx context.Context, companyCode string, trip TripRecord) (int64, error) {
	if s == nil || s.tenants == nil {
		return 0, fmt.Errorf("fleet store not initialised")
	}
	pool, err := s.tenants.DB(companyCode)
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.DB.QueryRowContext(ctx, `
		INSERT INTO th_vehicle_trips (
			company_code, vehicle_id, imei, start_time, end_time,
			start_lat, start_lon, end_lat, end_lon,
			distance_km, max_speed_kmh, avg_speed_kmh, stop_count, duration_seconds)
		VALUES ($1, $2, $3, $4, $5,
		        $6::float8, $7::float8, $8::float8, $9::float8,
		        $10::float8, $11::float8, $12::float8, $13, $14)
		RETURNING id`,
		trip.CompanyCode, trip.VehicleID, trip.IMEI, trip.StartTime.UTC(), trip.EndTime.UTC(),
		trip.StartLat, trip.StartLon, trip.EndLat, trip.EndLon,
		trip.DistanceKM, trip.MaxSpeedKMH, trip.AvgSpeedKMH, trip.StopCount, trip.DurationSeconds).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: insert trip vehicle %d: %w", trip.VehicleID, err)
	}
	return id, nil
}

// InsertStops stores the stops of a trip in one transaction (FR-2.6). An empty
// slice is a no-op. The trip id is validated inside the INSERT (SELECT ... FROM
// th_vehicle_trips) so a stop can never reference a foreign trip.
func (s *PostgresFleetStore) InsertStops(ctx context.Context, companyCode string, tripID int64, stops []StopRecord) error {
	if len(stops) == 0 {
		return nil
	}
	if s == nil || s.tenants == nil {
		return fmt.Errorf("fleet store not initialised")
	}
	if tripID <= 0 {
		return fmt.Errorf("store: insert stops without a trip id")
	}
	pool, err := s.tenants.DB(companyCode)
	if err != nil {
		return err
	}
	tx, err := pool.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin stops tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i := range stops {
		st := stops[i]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO td_vehicle_stops (
				trip_id, company_code, vehicle_id, start_time, end_time,
				duration_seconds, lat, lon)
			SELECT $1, $2, t.vehicle_id, $3, $4, $5, $6::float8, $7::float8
			  FROM th_vehicle_trips t WHERE t.id = $1`,
			tripID, companyCode, st.StartTime.UTC(), st.EndTime.UTC(),
			st.DurationSeconds, st.Lat, st.Lon); err != nil {
			return fmt.Errorf("store: insert stop (trip %d): %w", tripID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit stops trip %d: %w", tripID, err)
	}
	return nil
}

// Compile-time guarantee that the production store satisfies the interface.
var _ FleetStore = (*PostgresFleetStore)(nil)
