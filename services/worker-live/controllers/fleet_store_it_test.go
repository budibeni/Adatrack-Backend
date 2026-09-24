package controllers

// fleet_store_it_test.go — integration coverage for the B7.1/B7.2 persistence
// (PostgresFleetStore) against live PostgreSQL + the DEV001 tenant. Opt-in via
// ADATRACK_IT=1 (mirrors the other services' IT suites).
//
// The suite only ever moves the counters of vehicle 1 by a known amount and
// restores them afterwards, and it deletes the trip/stop rows it creates, so the
// shared dev dataset is left untouched.

import (
	"context"
	"os"
	"testing"
	"time"

	"adatrack_gps/internal"
	"adatrack_gps/internal/tenant"
)

// itCompany is the tenant seeded by database/seed + provision-tenant.sh.
const itCompany = "DEV001"

// itSkip gates the suite behind ADATRACK_IT=1.
func itSkip(t *testing.T) {
	t.Helper()
	if v, ok := os.LookupEnv("ADATRACK_IT"); ok && v == "1" {
		return
	}
	t.Skip("integration test — set ADATRACK_IT=1 with live PostgreSQL (127.0.0.1:5533)")
}

// newITFleetStore wires the real store on the DEV001 tenant.
func newITFleetStore(t *testing.T) (*PostgresFleetStore, context.Context) {
	t.Helper()
	itSkip(t)
	internal.LoadProjectEnv()
	t.Setenv("POSTGRES_HOST", envOrLocal("ADATRACK_IT_PG_HOST", "127.0.0.1"))
	t.Setenv("POSTGRES_PORT", envOrLocal("ADATRACK_IT_PG_PORT", "5533"))
	t.Setenv("REDIS_HOST", envOrLocal("ADATRACK_IT_REDIS_HOST", "127.0.0.1"))
	t.Setenv("REDIS_PORT", envOrLocal("ADATRACK_IT_REDIS_PORT", "6380"))

	cfg := internal.LoadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tm, err := tenant.New(ctx, cfg, tenant.ConfigFromEnv(cfg), nil, nil)
	if err != nil {
		t.Fatalf("tenant manager unavailable (is compose up + migrated?): %v", err)
	}
	t.Cleanup(tm.Close)

	return NewPostgresFleetStore(tm), context.Background()
}

// envOrLocal reads an env override or the documented dev default.
func envOrLocal(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// TestITAddMeteringAccumulates pins FR-2.5 against real PostgreSQL: the counters
// only ever grow by the flushed delta and `odometer_updated_at` is stamped.
func TestITAddMeteringAccumulates(t *testing.T) {
	store, ctx := newITFleetStore(t)
	pool, err := store.tenants.DB(itCompany)
	if err != nil {
		t.Fatalf("tenant pool: %v", err)
	}

	read := func() (float64, float64, bool) {
		var (
			odo, hours float64
			updatedAt  *time.Time
		)
		if err := pool.DB.QueryRowContext(ctx, `
			SELECT odometer_km::float8, engine_hours::float8, odometer_updated_at
			  FROM tm_vehicles WHERE id = 1`).Scan(&odo, &hours, &updatedAt); err != nil {
			t.Fatalf("read counters: %v", err)
		}
		return odo, hours, updatedAt != nil
	}

	beforeOdo, beforeHours, _ := read()
	t.Cleanup(func() {
		if _, err := pool.DB.ExecContext(context.Background(), `
			UPDATE tm_vehicles SET odometer_km = $2, engine_hours = $3 WHERE id = $1`,
			1, beforeOdo, beforeHours); err != nil {
			t.Logf("restore counters: %v", err)
		}
	})

	if err := store.AddMetering(ctx, itCompany, 1, 0.333, 0.0611); err != nil {
		t.Fatalf("AddMetering: %v", err)
	}
	odo, hours, stamped := read()
	if got := odo - beforeOdo; got < 0.332 || got > 0.334 {
		t.Fatalf("odometer delta = %.4f, want ≈0.333", got)
	}
	if got := hours - beforeHours; got < 0.0609 || got > 0.0613 {
		t.Fatalf("engine hours delta = %.4f, want ≈0.0611", got)
	}
	if !stamped {
		t.Fatal("odometer_updated_at was not stamped")
	}

	// Anti-rollback: a non-positive delta is a no-op, never a decrease.
	if err := store.AddMetering(ctx, itCompany, 1, -5, -1); err != nil {
		t.Fatalf("AddMetering(negative): %v", err)
	}
	afterOdo, _, _ := read()
	if afterOdo < odo {
		t.Fatalf("odometer decreased to %.4f (anti-rollback violated)", afterOdo)
	}

	// A soft-deleted / unknown vehicle is skipped without error.
	if err := store.AddMetering(ctx, itCompany, 0, 1, 1); err != nil {
		t.Fatalf("AddMetering(vehicle 0): %v", err)
	}
}

// TestITInsertTripAndStops pins FR-2.6 persistence: the trip header is inserted
// with its generated id and the stops reference it.
func TestITInsertTripAndStops(t *testing.T) {
	store, ctx := newITFleetStore(t)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	tripID, err := store.InsertTrip(ctx, itCompany, TripRecord{
		CompanyCode: itCompany, VehicleID: 1, IMEI: "864201040512345",
		StartTime: base, EndTime: base.Add(60 * time.Second),
		StartLat: -6.2, StartLon: 106.8, EndLat: -6.2005, EndLon: 106.8,
		DistanceKM: 0.111, MaxSpeedKMH: 45, AvgSpeedKMH: 6.66, StopCount: 1, DurationSeconds: 60,
	})
	if err != nil {
		t.Fatalf("InsertTrip: %v", err)
	}
	if tripID <= 0 {
		t.Fatalf("trip id = %d, want a generated id", tripID)
	}

	if err := store.InsertStops(ctx, itCompany, tripID, []StopRecord{{
		StartTime: base.Add(60 * time.Second), EndTime: base.Add(190 * time.Second),
		Lat: -6.2005, Lon: 106.8, DurationSeconds: 130,
	}}); err != nil {
		t.Fatalf("InsertStops: %v", err)
	}

	pool, err := store.tenants.DB(itCompany)
	if err != nil {
		t.Fatalf("tenant pool: %v", err)
	}
	t.Cleanup(func() {
		if _, derr := pool.DB.ExecContext(context.Background(),
			`DELETE FROM th_vehicle_trips WHERE id = $1`, tripID); derr != nil {
			t.Logf("delete trip: %v", derr)
		}
	})

	var (
		stops   int
		storedD float64
	)
	if err := pool.DB.QueryRowContext(ctx,
		`SELECT count(*) FROM td_vehicle_stops WHERE trip_id = $1`, tripID).Scan(&stops); err != nil {
		t.Fatalf("count stops: %v", err)
	}
	if stops != 1 {
		t.Fatalf("stops = %d, want 1", stops)
	}
	if err := pool.DB.QueryRowContext(ctx,
		`SELECT distance_km::float8 FROM th_vehicle_trips WHERE id = $1`, tripID).Scan(&storedD); err != nil {
		t.Fatalf("read trip: %v", err)
	}
	if storedD < 0.110 || storedD > 0.112 {
		t.Fatalf("stored distance = %.4f, want ≈0.111", storedD)
	}
}
