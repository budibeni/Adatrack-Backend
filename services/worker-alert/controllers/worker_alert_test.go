package controllers

import (
	"testing"
	"time"

	"ajb_gps/worker-alert/models"
)

// ---------------------------------------------------------------------------
// Geometry
// ---------------------------------------------------------------------------

func TestWithinRadius(t *testing.T) {
	// Depot Jakarta seed: center -6.2088,106.8456 r=800m.
	if !withinRadius(-6.2088, 106.8456, 800, -6.2090, 106.8458) {
		t.Fatal("point ~30m from center should be inside")
	}
	if withinRadius(-6.2088, 106.8456, 800, -6.2150, 106.8530) {
		t.Fatal("point >1km away should be outside")
	}
}

func TestPointInPolygon(t *testing.T) {
	square := [][2]float64{
		{0, 0}, {0, 10}, {10, 10}, {10, 0}, // {lat,lon}
	}
	if !pointInPolygon(5, 5, square) {
		t.Fatal("center point should be inside square polygon")
	}
	if pointInPolygon(15, 15, square) {
		t.Fatal("far point should be outside square polygon")
	}
	if !pointInPolygon(0.1, 9.9, square) {
		t.Fatal("near-corner point should be inside")
	}
}

func TestParseGeofenceGeometryCircle(t *testing.T) {
	coords := []byte(`{"type":"Point","coordinates":[106.8456,-6.2088]}`)
	lat, lon, ring, err := parseGeofenceGeometry("circle", coords, nil)
	if err != nil {
		t.Fatalf("parse circle: %v", err)
	}
	if lat != -6.2088 || lon != 106.8456 {
		t.Fatalf("unexpected center lat=%f lon=%f", lat, lon)
	}
	if ring != nil {
		t.Fatal("circle must not produce boundary ring")
	}
}

func TestParseGeofenceGeometryPolygonBoundary(t *testing.T) {
	coords := []byte(`{"type":"Polygon","coordinates":[[[106.0,-6.0],[107.0,-6.0],[107.0,-7.0],[106.0,-7.0],[106.0,-6.0]]]}`)
	boundary := []byte(`[{"lat":-6.1,"lon":106.1},{"lat":-6.1,"lon":106.9},{"lat":-6.9,"lon":106.9},{"lat":-6.9,"lon":106.1}]`)
	lat, lon, ring, err := parseGeofenceGeometry("polygon", coords, boundary)
	if err != nil {
		t.Fatalf("parse polygon: %v", err)
	}
	if len(ring) != 4 {
		t.Fatalf("boundary_points should override with 4 vertices, got %d", len(ring))
	}
	if ring[0][0] != -6.1 || ring[0][1] != 106.1 {
		t.Fatalf("boundary order should be {lat,lon}, got %v", ring[0])
	}
	_ = lat
	_ = lon
}

func TestParseGeofenceGeometryPolygonGeoJSONFallback(t *testing.T) {
	coords := []byte(`{"type":"Polygon","coordinates":[[[106.0,-6.0],[107.0,-6.0],[107.0,-7.0],[106.0,-7.0],[106.0,-6.0]]]}`)
	_, _, ring, err := parseGeofenceGeometry("polygon", coords, nil)
	if err != nil {
		t.Fatalf("parse fallback polygon: %v", err)
	}
	// 5 vertex termasuk closing point.
	if len(ring) != 5 {
		t.Fatalf("geojson outer ring expected 5 points, got %d", len(ring))
	}
	if ring[0][0] != -6.0 || ring[0][1] != 106.0 {
		t.Fatalf("fallback ring should be {lat,lon} = {-6.0,106.0}, got %v", ring[0])
	}
}

// ---------------------------------------------------------------------------
// Severity & subjects
// ---------------------------------------------------------------------------

func TestSeverityRank(t *testing.T) {
	cases := map[string]int{
		"low": 0, "info": 0,
		"medium": 1, "warning": 1,
		"high": 2, "critical": 2,
		"unknown": 1,
	}
	for sev, want := range cases {
		if got := severityRank(sev); got != want {
			t.Errorf("severityRank(%q)=%d want %d", sev, got, want)
		}
	}
	// critical alert melewati min_severity warning; sebaliknya tidak.
	if severityRank("critical") < severityRank("warning") {
		t.Fatal("critical must outrank warning")
	}
	if severityRank("low") >= severityRank("warning") {
		t.Fatal("low must not outrank warning")
	}
}

func TestAlertPrefTypesAndSubjects(t *testing.T) {
	if got := alertPrefTypes(models.AlertTypeSOS); got[0] != "sos" {
		t.Errorf("sos pref type = %v", got)
	}
	if got := alertTypeSubject(models.AlertTypeRouteDev); got != "route_deviation" {
		t.Errorf("route_deviation subject = %q", got)
	}
	if got := alertTypeSubject(models.AlertTypeSpeed); got != "speed" {
		t.Errorf("speed subject = %q", got)
	}
}

func TestLowerCode(t *testing.T) {
	if lowerCode("DEV001") != "dev001" {
		t.Fatalf("lowerCode = %q", lowerCode("DEV001"))
	}
}

func TestNormalizeSeverity(t *testing.T) {
	cases := map[string]string{
		"warning": "medium", "info": "medium", "": "medium",
		"low": "low", "medium": "medium", "high": "high", "critical": "critical",
		"weird": "medium",
	}
	for in, want := range cases {
		if got := normalizeSeverity(in); got != want {
			t.Errorf("normalizeSeverity(%q)=%q want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Route tracking math
// ---------------------------------------------------------------------------

func TestNearestDistance(t *testing.T) {
	wps := []models.Waypoint{{Lat: 0, Lon: 0}, {Lat: 0.01, Lon: 0}}
	d, idx := nearestDistance(0.001, 0.001, wps)
	if idx != 0 {
		t.Fatalf("nearest index = %d want 0", idx)
	}
	if d <= 0 || d > 500 {
		t.Fatalf("distance %.1f m out of expected range", d)
	}
}

func TestHaversineMetersKnownDistance(t *testing.T) {
	// ~111.19 km per derajat latitude di ekuator.
	d := haversineMeters(0, 100, 1, 100)
	if d < 110000 || d > 112500 {
		t.Fatalf("haversine 1° lat = %.0f m", d)
	}
}

// ---------------------------------------------------------------------------
// Env helpers
// ---------------------------------------------------------------------------

func TestDurationFromEnvDefaults(t *testing.T) {
	t.Setenv("TEST_DURATION_MINUTES", "")
	if d := durationFromEnv("TEST_DURATION_MINUTES", 3*time.Minute); d != 3*time.Minute {
		t.Fatalf("default = %s", d)
	}
	t.Setenv("TEST_DURATION_MINUTES", "90s")
	if d := durationFromEnv("TEST_DURATION_MINUTES", 3*time.Minute); d != 90*time.Second {
		t.Fatalf("duration string = %s", d)
	}
	t.Setenv("TEST_DURATION_MINUTES", "5")
	if d := durationFromEnv("TEST_DURATION_MINUTES", 3*time.Minute); d != 5*time.Minute {
		t.Fatalf("plain minutes = %s", d)
	}
}

func TestSplitSubject(t *testing.T) {
	parts := splitSubject("telemetry.raw.864201040512345")
	if len(parts) != 3 || parts[2] != "864201040512345" {
		t.Fatalf("splitSubject = %v", parts)
	}
}

func TestDecodeStatePayload(t *testing.T) {
	val := `{"lat":-6.2,"lon":106.8,"timestamp":1723800000}`
	m, err := decodeState(val)
	if err != nil {
		t.Fatalf("decodeState: %v", err)
	}
	ts, ok := m["timestamp"].(float64)
	if !ok || ts != 1723800000 {
		t.Fatalf("timestamp = %v ok=%v", m["timestamp"], ok)
	}
}
