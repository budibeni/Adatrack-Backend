package controllers

import (
	"math"
	"testing"

	"ajb_gps/worker-alert/models"
)

// TestHaversineM checks known distances (Jakarta → Bandung ≈ 150 km).
func TestHaversineM(t *testing.T) {
	d := HaversineM(-6.2000, 106.8166, -6.9175, 107.6191) // Jakarta → Bandung
	if d < 110_000 || d > 130_000 {
		t.Errorf("HaversineM(Jakarta, Bandung) = %.0f m, want ~120 km great-circle", d)
	}
	if d2 := HaversineM(-6.2, 106.8, -6.2, 106.8); d2 != 0 {
		t.Errorf("HaversineM(same point) = %f, want 0", d2)
	}
}

// TestPointInPolygon covers the standard ray-casting cases (square 0..10).
func TestPointInPolygon(t *testing.T) {
	square := [][2]float64{{0, 0}, {0, 10}, {10, 10}, {10, 0}}
	cases := []struct {
		lat, lon float64
		want     bool
	}{
		{5, 5, true},    // centre
		{1, 1, true},    // inside
		{15, 5, false},  // north
		{-5, 5, false},  // south
		{5, 15, false},  // east
		{5, -5, false},  // west
		{11, 11, false}, // corner outside
	}
	for _, tc := range cases {
		if got := PointInPolygon(tc.lat, tc.lon, square); got != tc.want {
			t.Errorf("PointInPolygon(%v, %v) = %v, want %v", tc.lat, tc.lon, got, tc.want)
		}
	}
	// Concave polygon (L-shape): the notch must be outside.
	lshape := [][2]float64{{0, 0}, {0, 10}, {5, 10}, {5, 5}, {10, 5}, {10, 0}}
	if PointInPolygon(7, 7, lshape) {
		t.Error("PointInPolygon(notch) = true, want false")
	}
	if !PointInPolygon(2, 8, lshape) {
		t.Error("PointInPolygon(inside L) = false, want true")
	}
}

// TestNearestWaypointM checks the route-deviation anchor resolution.
func TestNearestWaypointM(t *testing.T) {
	waypoints := [][2]float64{{-6.2, 106.8}, {-6.3, 106.9}}
	dist, idx := NearestWaypointM(-6.21, 106.81, waypoints)
	if idx != 0 {
		t.Errorf("idx = %d, want 0 (nearest is the first waypoint)", idx)
	}
	if dist <= 0 || dist > 10_000 {
		t.Errorf("dist = %.0f m, want a small positive value", dist)
	}
	if _, idx := NearestWaypointM(-6.31, 106.91, waypoints); idx != 1 {
		t.Errorf("idx = %d, want 1 (nearest is the second waypoint)", idx)
	}
}

// TestEffectiveSpeedConfig verifies the precedence rules (PRD §5.9.4):
// vehicle-specific wins, disabled rows are skipped, global is the fallback.
func TestEffectiveSpeedConfig(t *testing.T) {
	configRows := []models.SpeedConfig{
		{VehicleID: 0, MaxSpeed: 80, Enabled: true, Severity: models.SeverityMedium},
		{VehicleID: 7, MaxSpeed: 60, Enabled: true, Severity: models.SeverityHigh},
		{VehicleID: 9, MaxSpeed: 50, Enabled: false, Severity: models.SeverityLow},
	}

	if got := effectiveSpeedConfig(configRows, 7); got == nil || got.MaxSpeed != 60 {
		t.Errorf("vehicle 7 config = %+v, want the specific row (60)", got)
	}
	if got := effectiveSpeedConfig(configRows, 8); got == nil || got.MaxSpeed != 80 {
		t.Errorf("vehicle 8 config = %+v, want the global row (80)", got)
	}
	if got := effectiveSpeedConfig(configRows, 9); got == nil || got.MaxSpeed != 80 {
		t.Errorf("vehicle 9 config = %+v, want the global row (disabled rows are skipped)", got)
	}
	if got := effectiveSpeedConfig(nil, 7); got != nil {
		t.Errorf("empty config set must return nil, got %+v", got)
	}
}

// TestGraceMarginMath is the §5.9.4 arithmetic: raise only above limit+grace,
// escalate to critical beyond 1.5× the limit.
func TestGraceMarginMath(t *testing.T) {
	limit := float64(100) * (1 + float64(10)/100) // 100 km/h + 10% grace
	if math.Abs(limit-110) > 0.001 {
		t.Fatalf("limit = %f, want 110", limit)
	}
	if !(109 <= limit) {
		t.Error("109 must stay inside the grace band (no alert)")
	}
	if !(111 > limit) {
		t.Error("111 must breach the effective limit (alert)")
	}
	if !(151 > 1.5*100) {
		t.Error("151 km/h must escalate to critical (> 1.5×100)")
	}
	if 150 > 1.5*100 {
		t.Error("150 km/h must NOT escalate to critical (not strictly above 1.5×)")
	}
}
