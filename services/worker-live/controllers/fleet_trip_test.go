package controllers

import (
	"testing"
	"time"
)

// TestTripAndStopDetection drives the FR-2.6 machine through one full cycle:
// movement → confirmed stop (≥ TRIP_MIN_STOP_SECONDS) → movement again. The trip
// must close at the stop, carry the stop, and a new trip must open on resume.
func TestTripAndStopDetection(t *testing.T) {
	acc := testFleetAccumulator()
	base := time.Date(2026, 9, 24, 6, 0, 0, 0, time.UTC)
	on := accOnPtr(true)

	// Moving: 3 fixes, 20 s apart, 0.001° of latitude each (≈110 m).
	acc.Observe(fleetFix(base, -6.2000, 106.8000, 40, on), base)
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.2010, 106.8000, 45, on), base)
	acc.Observe(fleetFix(base.Add(40*time.Second), -6.2020, 106.8000, 30, on), base)

	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}
	if !acc.states[key].trip.open {
		t.Fatal("movement must open a trip")
	}

	// Stationary: the stop is only confirmed after the 30 s grace window.
	acc.Observe(fleetFix(base.Add(60*time.Second), -6.2020, 106.8000, 0, on), base)
	acc.Observe(fleetFix(base.Add(100*time.Second), -6.2020, 106.8000, 0, on), base)
	if !acc.states[key].trip.stopConfirmed {
		t.Fatal("a 40 s standstill must confirm the stop (grace 30 s)")
	}

	// Still standing: the stop ends at the LAST stationary fix (130 s ≥ min 60 s).
	acc.Observe(fleetFix(base.Add(190*time.Second), -6.2020, 106.8000, 0, on), base)

	// Movement resumes → the trip closes and a new one opens.
	acc.Observe(fleetFix(base.Add(200*time.Second), -6.2030, 106.8000, 35, on), base)

	_, trips := acc.Drain(base.Add(5 * time.Minute))
	if len(trips) != 1 {
		t.Fatalf("closed trips = %d, want 1", len(trips))
	}
	trip := trips[0].trip
	if !trip.StartTime.Equal(base) {
		t.Errorf("trip start = %s, want %s", trip.StartTime, base)
	}
	if want := base.Add(60 * time.Second); !trip.EndTime.Equal(want) {
		t.Errorf("trip end = %s, want the stop start %s", trip.EndTime, want)
	}
	if trip.DurationSeconds != 60 {
		t.Errorf("trip duration = %d s, want 60", trip.DurationSeconds)
	}
	if trip.DistanceKM < 0.21 || trip.DistanceKM > 0.33 {
		t.Errorf("trip distance = %.3f km, want ≈0.22 km", trip.DistanceKM)
	}
	if trip.MaxSpeedKMH != 45 {
		t.Errorf("trip max speed = %.1f, want 45", trip.MaxSpeedKMH)
	}
	if trip.StopCount != 1 || len(trips[0].stops) != 1 {
		t.Fatalf("trip stops = %d (records %d), want 1", trip.StopCount, len(trips[0].stops))
	}
	stop := trips[0].stops[0]
	if !stop.StartTime.Equal(base.Add(60*time.Second)) || !stop.EndTime.Equal(base.Add(190*time.Second)) {
		t.Errorf("stop window = %s → %s, want 60 s → 190 s", stop.StartTime, stop.EndTime)
	}
	if stop.DurationSeconds != 130 {
		t.Errorf("stop duration = %d s, want 130", stop.DurationSeconds)
	}
	if trip.AvgSpeedKMH <= 0 {
		t.Errorf("trip average speed = %.2f, want > 0", trip.AvgSpeedKMH)
	}
	if !acc.states[key].trip.open {
		t.Error("movement after the stop must open the next trip")
	}
	if got := acc.states[key].trip.stops; len(got) != 0 {
		t.Errorf("the new trip must start with an empty stop list, got %d", len(got))
	}
}

// accOnPtr builds the tri-state ACC used by the FR-2.6 tests.
func accOnPtr(v bool) *bool { return &v }

// TestShortStopIsSwallowed documents TRIP_MIN_STOP_SECONDS: a confirmed stop
// shorter than the minimum is a blip — no stop row, no trip split.
func TestShortStopIsSwallowed(t *testing.T) {
	acc := testFleetAccumulator()
	base := time.Date(2026, 9, 24, 7, 0, 0, 0, time.UTC)

	acc.Observe(fleetFix(base, -6.2000, 106.8000, 30, nil), base)
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.2010, 106.8000, 30, nil), base)
	// Stationary long enough for the grace window but shorter than 60 s.
	acc.Observe(fleetFix(base.Add(30*time.Second), -6.2010, 106.8000, 0, nil), base)
	acc.Observe(fleetFix(base.Add(65*time.Second), -6.2010, 106.8000, 0, nil), base)
	// Resume before the stop reaches TRIP_MIN_STOP_SECONDS.
	acc.Observe(fleetFix(base.Add(75*time.Second), -6.2020, 106.8000, 30, nil), base)

	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}
	if !acc.states[key].trip.open {
		t.Fatal("a swallowed stop must keep the same trip open")
	}
	if acc.states[key].trip.stopConfirmed {
		t.Error("resuming within TRIP_MIN_STOP_SECONDS must clear the stop candidate")
	}
	if _, trips := acc.Drain(base.Add(2 * time.Minute)); len(trips) != 0 {
		t.Fatalf("trips = %d, want 0 (no trip split)", len(trips))
	}
	if got := acc.states[key].trip.stops; len(got) != 0 {
		t.Errorf("stops = %+v, want none", got)
	}
}

// TestAutoCloseAfterMaxStop covers the FR-2.6 auto-close: a vehicle that stands
// (or stops reporting) longer than TRIP_MAX_STOP_SECONDS closes its trip on the
// next flush, recording the confirmed stop.
func TestAutoCloseAfterMaxStop(t *testing.T) {
	acc := testFleetAccumulator()
	acc.p.maxStop = 10 * time.Minute // keep the test fast, still > minStop
	base := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)

	acc.Observe(fleetFix(base, -6.2000, 106.8000, 30, nil), base)
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.2010, 106.8000, 30, nil), base)
	// Standstill: confirmed after the grace window, still standing at +300 s.
	acc.Observe(fleetFix(base.Add(30*time.Second), -6.2010, 106.8000, 0, nil), base)
	acc.Observe(fleetFix(base.Add(300*time.Second), -6.2010, 106.8000, 0, nil), base)

	// The flush happens long after the vehicle went silent.
	_, trips := acc.Drain(base.Add(2 * time.Hour))
	if len(trips) != 1 {
		t.Fatalf("auto-closed trips = %d, want 1", len(trips))
	}
	trip := trips[0].trip
	if want := base.Add(30 * time.Second); !trip.EndTime.Equal(want) {
		t.Errorf("trip end = %s, want the stop start %s", trip.EndTime, want)
	}
	if len(trips[0].stops) != 1 {
		t.Fatalf("stops = %d, want 1", len(trips[0].stops))
	}
	if got := trips[0].stops[0].DurationSeconds; got != 270 {
		t.Errorf("stop duration = %d s, want 270 (30 s → 300 s)", got)
	}
	if acc.states[fleetKey{CompanyCode: "DEV001", VehicleID: 1}].trip.open {
		t.Error("the auto-closed trip must not stay open")
	}
}

// TestRequeueKeepsFailedWork asserts the buffer contract used by the flusher: a
// failed persistence attempt is re-buffered, never dropped.
func TestRequeueKeepsFailedWork(t *testing.T) {
	acc := testFleetAccumulator()
	base := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)

	acc.Observe(fleetFix(base, -6.2000, 106.8000, 30, nil), base)
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.2010, 106.8000, 30, nil), base)

	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}
	if acc.Pending() == 0 {
		t.Fatal("Pending must report the buffered odometer delta")
	}
	deltas, trips := acc.Drain(base.Add(time.Minute))
	if len(deltas) != 1 || len(trips) != 0 {
		t.Fatalf("drain = %d deltas / %d trips, want 1/0", len(deltas), len(trips))
	}
	if acc.Pending() != 0 {
		t.Fatal("Drain must clear the buffers")
	}

	acc.RequeueMetering(key, deltas[key])
	if acc.Pending() != 1 {
		t.Fatalf("Pending = %d after requeue, want 1", acc.Pending())
	}
	acc.RequeueTrips([]pendingTrip{{key: key, trip: TripRecord{VehicleID: 1}, tripID: 7}})
	if acc.Pending() != 2 {
		t.Fatalf("Pending = %d after trip requeue, want 2", acc.Pending())
	}
	deltas2, trips2 := acc.Drain(base.Add(2 * time.Minute))
	if len(deltas2) != 1 || len(trips2) != 1 {
		t.Fatalf("second drain = %d deltas / %d trips, want 1/1", len(deltas2), len(trips2))
	}
	if trips2[0].tripID != 7 {
		t.Errorf("requeued trip id = %d, want 7 (stops-only retry)", trips2[0].tripID)
	}
}
