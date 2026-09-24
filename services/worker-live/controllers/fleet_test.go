package controllers

import (
	"testing"
	"time"

	"adatrack_gps/worker-live/models"
)

// testFleetParams returns the PRD thresholds (FR-2.5/FR-2.6) with a short flush
// cadence so the tests never depend on wall-clock timing.
func testFleetParams() fleetParams {
	return fleetParams{
		flushEvery:     time.Second,
		flushBatch:     100,
		maxJumpKM:      5,
		maxGap:         5 * time.Minute,
		engineMaxGap:   5 * time.Minute,
		stopGrace:      30 * time.Second,
		minStop:        time.Minute,
		maxStop:        time.Hour,
		movingSpeedKMH: 0,
	}.withDefaults()
}

// fleetFix builds one positioned telemetry message of tenant DEV001.
func fleetFix(ts time.Time, lat, lon, speed float64, acc *bool) models.TelemetryMessage {
	return models.TelemetryMessage{
		IMEI: "864201040512345", CompanyCode: "DEV001", VehicleID: 1,
		Lat: lat, Lon: lon, Speed: speed, ACC: acc, Fix: true, Timestamp: ts.Unix(),
	}
}

// TestOdometerAccumulatesHaversine covers FR-2.5: the odometer follows the
// Haversine delta of consecutive fixes and engine hours follow the ACC-gated
// interval time.
func TestOdometerAccumulatesHaversine(t *testing.T) {
	acc := testFleetAccumulator()
	base := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	on := models.BoolPtr(true)

	acc.Observe(fleetFix(base, -6.2000, 106.8000, 40, on), base)
	if acc.Pending() != 0 {
		t.Fatal("the first fix has no predecessor and must not accumulate")
	}
	// 0.01° of latitude ≈ 1.103 km (Haversine at the equator radius 6371 km).
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.2100, 106.8000, 40, on), base)
	acc.Observe(fleetFix(base.Add(40*time.Second), -6.2200, 106.8000, 40, on), base)

	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}
	if got := acc.states[key].odoKM; got < 2.15 || got > 2.30 {
		t.Fatalf("odometer = %.4f km, want ≈2.21 km (2 × 1.105 km)", got)
	}
	// Two 20 s intervals with ACC ON = 40 s = 0.0111 h.
	if got := acc.states[key].engineHours; got < 0.0105 || got > 0.0115 {
		t.Fatalf("engine hours = %.4f, want ≈0.0111 (40 s)", got)
	}

	deltas, trips := acc.Drain(base.Add(time.Minute))
	if len(trips) != 0 {
		t.Fatalf("trips = %+v, want none (the trip is still open)", trips)
	}
	d, ok := deltas[key]
	if !ok {
		t.Fatal("the drained batch must carry the vehicle delta")
	}
	if d.OdometerKM < 2.15 || d.EngineHours <= 0 {
		t.Fatalf("delta = %+v, want the accumulated distance + engine time", d)
	}
	// Drain clears the buffer (the values are now owned by the caller).
	if acc.Pending() != 0 {
		t.Fatal("Drain must clear the pending metering")
	}
	if again, _ := acc.Drain(base); len(again) != 0 {
		t.Fatalf("second Drain = %+v, want an empty batch", again)
	}
}

// TestOdometerGuards covers the FR-2.5 guards: GPS jumps, over-long intervals,
// positionless frames, unrouted messages and out-of-order timestamps.
func TestOdometerGuards(t *testing.T) {
	acc := testFleetAccumulator()
	base := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}

	acc.Observe(fleetFix(base, -6.2000, 106.8000, 30, nil), base)

	// GPS jump: 0.1° of latitude ≈ 11 km > 5 km.
	acc.Observe(fleetFix(base.Add(20*time.Second), -6.3000, 106.8000, 30, nil), base)
	if got := acc.states[key].odoKM; got != 0 {
		t.Fatalf("odometer = %.4f, want 0 (GPS jump must be discarded)", got)
	}
	if acc.skippedJumps != 1 {
		t.Fatalf("skippedJumps = %d, want 1", acc.skippedJumps)
	}

	// Interval too long: 10 min > ODOMETER_MAX_GAP_SECONDS (5 min).
	acc.Observe(fleetFix(base.Add(10*time.Minute), -6.2010, 106.8000, 30, nil), base)
	if got := acc.states[key].odoKM; got != 0 {
		t.Fatalf("odometer = %.4f, want 0 (stale interval must not accumulate)", got)
	}

	// Out-of-order timestamp: no negative delta.
	acc.Observe(fleetFix(base.Add(9*time.Minute), -6.2020, 106.8000, 30, nil), base)
	if got := acc.states[key].odoKM; got < 0 {
		t.Fatalf("odometer = %.4f, want >= 0 (anti-rollback)", got)
	}

	// Positionless (fuel-only/heartbeat) and unrouted messages contribute nothing.
	acc.Observe(fleetFix(base.Add(12*time.Minute), -6.2020, 106.8000, 30, nil), base)
	before := acc.states[key].odoKM
	fuelLevel := 50.0
	fuel := models.TelemetryMessage{IMEI: "86001", CompanyCode: "DEV001", VehicleID: 1,
		FuelLevel: &fuelLevel, Timestamp: base.Add(13 * time.Minute).Unix()}
	acc.Observe(fuel, base.Add(13*time.Minute))
	unrouted := fleetFix(base.Add(14*time.Minute), -6.2040, 106.8000, 30, nil)
	unrouted.VehicleID = 0
	acc.Observe(unrouted, base)
	if acc.states[key].odoKM != before {
		t.Fatalf("positionless/unrouted frames must not move the odometer (%.4f → %.4f)",
			before, acc.states[key].odoKM)
	}
	if acc.skippedNoIDs != 1 {
		t.Fatalf("skippedNoIDs = %d, want 1 (VehicleID=0 guard)", acc.skippedNoIDs)
	}
}

// TestEngineHoursOnlyWithDeviceACC asserts the B6/B7 interaction: engine time is
// credited only for intervals whose preceding fix confirmed ACC ON.
func TestEngineHoursOnlyWithDeviceACC(t *testing.T) {
	base := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	key := fleetKey{CompanyCode: "DEV001", VehicleID: 1}

	cases := []struct {
		name string
		acc  *bool
		want bool
	}{
		{"unreported ACC never invents engine time", nil, false},
		{"ACC off is a literal device reading", models.BoolPtr(false), false},
		{"ACC on credits the interval", models.BoolPtr(true), true},
	}
	for _, tc := range cases {
		acc := testFleetAccumulator()
		acc.Observe(fleetFix(base, -6.2, 106.8, 20, tc.acc), base)
		acc.Observe(fleetFix(base.Add(time.Minute), -6.201, 106.8, 20, tc.acc), base)
		got := acc.states[key].engineHours > 0
		if got != tc.want {
			t.Errorf("%s: engine hours credited = %v, want %v (%.5f h)",
				tc.name, got, tc.want, acc.states[key].engineHours)
		}
	}
}

// testFleetAccumulator builds an accumulator with the PRD test thresholds.
func testFleetAccumulator() *fleetAccumulator {
	return newFleetAccumulator(testFleetParams())
}
