package controllers

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"adatrack_gps/service-websocket/models"
)

// playbackRoute builds a straight-ish route of `n` points around Jakarta.
func playbackRoute(n int, start time.Time) []models.Position {
	out := make([]models.Position, 0, n)
	for i := 0; i < n; i++ {
		acc := i%2 == 0
		out = append(out, models.Position{
			Timestamp: start.Add(time.Duration(i) * 20 * time.Second).Format(time.RFC3339),
			Lat:       -6.2000 - float64(i)*0.0005,
			Lon:       106.8000 + float64(i)*0.0002,
			Speed:     30 + float64(i%5),
			Heading:   90,
			ACC:       &acc,
			Battery:   80,
			Fix:       true,
		})
	}
	return out
}

// dataOf decodes the PRD §8.1 `data` block of a response.
func dataOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("response body has no data block: %v", body)
	}
	return data
}

// pointsOf decodes the playback point list.
func pointsOf(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, ok := data["points"].([]any)
	if !ok {
		t.Fatalf("data.points is not a list: %v", data)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		point, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("point is not an object: %v", item)
		}
		out = append(out, point)
	}
	return out
}

// TestPlaybackReducesRouteAndKeepsShape covers B7.4: the endpoint returns a
// REDUCED route (fewer points, endpoints intact) plus the reduction statistics
// and the route distance measured on the original sequence.
func TestPlaybackReducesRouteAndKeepsShape(t *testing.T) {
	h := newHarness(t)
	route := playbackRoute(200, time.Now().UTC().Add(-time.Hour))
	h.playback.add("DEV001", 1, route...)

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet,
		"/api/v1/vehicles/1/playback?tolerance_m=25", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("playback status = %d body %v", resp.StatusCode, body)
	}
	data := dataOf(t, body)
	points := pointsOf(t, data)
	if len(points) < 2 {
		t.Fatalf("returned %d points, want at least the two endpoints", len(points))
	}
	total := int(data["total_points"].(float64))
	returned := int(data["returned_points"].(float64))
	if total != 200 {
		t.Fatalf("total_points = %d, want 200", total)
	}
	if returned != len(points) {
		t.Fatalf("returned_points = %d, points = %d", returned, len(points))
	}
	if returned >= total {
		t.Fatalf("returned %d of %d points: the nearly straight route was not reduced", returned, total)
	}
	if pct := data["reduction_percent"].(float64); pct <= 0 {
		t.Fatalf("reduction_percent = %v, want > 0", pct)
	}
	if dist := data["distance_km"].(float64); dist <= 0 {
		t.Fatalf("distance_km = %v, want > 0", dist)
	}
	// Endpoints must survive the reduction (route shape preserved).
	if got := points[0]["timestamp"]; got != route[0].Timestamp {
		t.Errorf("first point = %v, want the route start %s", got, route[0].Timestamp)
	}
	if got := points[len(points)-1]["timestamp"]; got != route[len(route)-1].Timestamp {
		t.Errorf("last point = %v, want the route end %s", got, route[len(route)-1].Timestamp)
	}
}

// TestPlaybackRBACAndValidation covers the contract guards of the endpoint.
func TestPlaybackRBACAndValidation(t *testing.T) {
	h := newHarness(t)
	h.playback.add("DEV001", 1, playbackRoute(10, time.Now().UTC().Add(-time.Hour))...)

	admin, _ := h.login(t, "admin@dev001.io", "Admin@123")
	operator, _ := h.login(t, "operator@dev001.io", "Admin@123")

	// Vehicle 3 belongs to the driver, not to the operator (row-level RBAC).
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/3/playback", operator, nil)
	if resp.StatusCode != http.StatusForbidden || errorCode(t, body) != CodeUnauthorizedVehicle {
		t.Fatalf("operator/vehicle 3 = %d %s, want 403 %s", resp.StatusCode,
			errorCode(t, body), CodeUnauthorizedVehicle)
	}

	// Unknown vehicle → 404.
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles/999/playback", admin, nil)
	if resp.StatusCode != http.StatusNotFound || errorCode(t, body) != CodeVehicleNotFound {
		t.Fatalf("unknown vehicle = %d %s, want 404 %s", resp.StatusCode,
			errorCode(t, body), CodeVehicleNotFound)
	}

	// Unauthenticated → 401.
	resp, _ = h.do(t, http.MethodGet, "/api/v1/vehicles/1/playback", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d, want 401", resp.StatusCode)
	}

	// Invalid tolerance → 400 VALIDATION_ERROR.
	for _, tol := range []string{"-1", "abc", "5000"} {
		resp, body = h.do(t, http.MethodGet,
			"/api/v1/vehicles/1/playback?tolerance_m="+tol, admin, nil)
		if resp.StatusCode != http.StatusBadRequest || errorCode(t, body) != CodeValidationError {
			t.Fatalf("tolerance_m=%s = %d %s, want 400 %s", tol, resp.StatusCode,
				errorCode(t, body), CodeValidationError)
		}
	}

	// Range beyond HISTORY_MAX_RANGE_DAYS → 400.
	old := time.Now().UTC().AddDate(0, 0, -400).Format(time.RFC3339)
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles/1/playback?from="+old, admin, nil)
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, body) != CodeValidationError {
		t.Fatalf("range guard = %d %s, want 400 %s", resp.StatusCode,
			errorCode(t, body), CodeValidationError)
	}
}

// TestPlaybackTruncatesAtTheCap asserts the documented truncation behaviour:
// `truncated=true` and at most PLAYBACK_MAX_POINTS are returned.
func TestPlaybackTruncatesAtTheCap(t *testing.T) {
	h := newHarnessWith(t, func(s *Settings) { s.PlaybackMaxPoints = 5 })
	h.playback.add("DEV001", 1, playbackRoute(50, time.Now().UTC().Add(-time.Hour))...)

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1/playback?tolerance_m=0", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body %v", resp.StatusCode, body)
	}
	data := dataOf(t, body)
	if data["truncated"] != true {
		t.Fatalf("truncated = %v, want true", data["truncated"])
	}
	if total := int(data["total_points"].(float64)); total != 5 {
		t.Fatalf("total_points = %d, want the cap 5", total)
	}
	if points := pointsOf(t, data); len(points) != 5 {
		t.Fatalf("points = %d, want 5 (tolerance_m=0 disables the reduction)", len(points))
	}
}

// TestPlaybackGeocodesEndpoints covers the B7.3 integration: the start/end of the
// route carry the offline address, and a coordinate outside the reference data
// stays without one (fallback, not an error).
func TestPlaybackGeocodesEndpoints(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()
	h.playback.add("DEV001", 1, []models.Position{
		{Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339), Lat: -6.1754, Lon: 106.8272, Speed: 20, Fix: true},
		{Timestamp: now.Add(-time.Hour).Format(time.RFC3339), Lat: -6.1755, Lon: 106.8273, Speed: 25, Fix: true},
		// Bali: no reference row within the search radius.
		{Timestamp: now.Format(time.RFC3339), Lat: -8.6500, Lon: 115.2167, Speed: 0, Fix: true},
	}...)

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1/playback?tolerance_m=0", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body %v", resp.StatusCode, body)
	}
	points := pointsOf(t, dataOf(t, body))
	if len(points) != 3 {
		t.Fatalf("points = %d, want 3 (tolerance 0)", len(points))
	}
	if got := points[0]["address"]; got != "Kota Jakarta Pusat, DKI Jakarta" {
		t.Errorf("first address = %v, want the Jakarta reference row", got)
	}
	if got := points[0]["city"]; got != "Kota Jakarta Pusat" {
		t.Errorf("first city = %v", got)
	}
	if got, ok := points[2]["address"]; ok {
		t.Errorf("unresolvable point address = %v, want it omitted", got)
	}
}

// TestReverseGeocodeEndpoint covers B7.3 as a standalone endpoint.
func TestReverseGeocodeEndpoint(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/geocode/reverse?lat=-6.1754&lon=106.8272", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body %v", resp.StatusCode, body)
	}
	addr, ok := dataOf(t, body)["address"].(map[string]any)
	if !ok {
		t.Fatalf("no address block: %v", body)
	}
	if addr["resolved"] != true || addr["address"] != "Kota Jakarta Pusat, DKI Jakarta" {
		t.Fatalf("address = %v, want the resolved Jakarta row", addr)
	}
	if addr["level"] != "city" {
		t.Errorf("level = %v, want city", addr["level"])
	}

	// Fallback: nothing in range → resolved=false, empty address, HTTP 200.
	resp, body = h.do(t, http.MethodGet, "/api/v1/geocode/reverse?lat=-8.65&lon=115.2167", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unresolvable status = %d, want 200 (documented fallback)", resp.StatusCode)
	}
	addr = dataOf(t, body)["address"].(map[string]any)
	if addr["resolved"] != false || addr["address"] != "" {
		t.Fatalf("unresolvable address = %v, want resolved=false", addr)
	}

	// Validation: missing/invalid/out-of-range coordinates → 400.
	for _, query := range []string{"", "?lat=-6.2", "?lat=abc&lon=106.8", "?lat=95&lon=106.8", "?lat=-6.2&lon=200"} {
		resp, body = h.do(t, http.MethodGet, "/api/v1/geocode/reverse"+query, access, nil)
		if resp.StatusCode != http.StatusBadRequest || errorCode(t, body) != CodeValidationError {
			t.Fatalf("query %q = %d %s, want 400 %s", query, resp.StatusCode,
				errorCode(t, body), CodeValidationError)
		}
	}

	// Unauthenticated → 401.
	resp, _ = h.do(t, http.MethodGet, "/api/v1/geocode/reverse?lat=-6.2&lon=106.8", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d, want 401", resp.StatusCode)
	}
}

// TestGeocoderCacheAndFallback unit-tests the cache layers of B7.3: the region
// reference is loaded once and reused, and a broken/empty reference never fails
// a lookup (it answers "unknown" instead).
func TestGeocoderCacheAndFallback(t *testing.T) {
	regions := newFakeRegions()
	g := newGeocoder(Settings{GeocodeCacheTTL: time.Minute, GeocodeIndexRefresh: time.Minute}, regions, nil)

	addr, ok := g.Reverse(context.Background(), -6.1754, 106.8272)
	if !ok || addr.City != "Kota Jakarta Pusat" {
		t.Fatalf("first lookup = %+v ok=%v", addr, ok)
	}
	if regions.callCount() != 1 {
		t.Fatalf("reference loads = %d, want 1 (index cached)", regions.callCount())
	}
	// A second (slightly different) coordinate reuses the in-process cache.
	if _, ok := g.Reverse(context.Background(), -6.1755, 106.8273); !ok {
		t.Fatal("nearby lookup must resolve")
	}
	if regions.callCount() != 1 {
		t.Fatalf("reference loads = %d after a cached lookup, want 1", regions.callCount())
	}
	if g.indexSize() == 0 {
		t.Fatal("indexSize must report the loaded centroids")
	}

	// Empty reference data → the documented fallback (no error, no address).
	empty := newFakeRegions()
	empty.set()
	g2 := newGeocoder(Settings{}, empty, nil)
	if addr, ok := g2.Reverse(context.Background(), -6.1754, 106.8272); ok || !addr.Empty() {
		t.Fatalf("empty reference = %+v ok=%v, want the unresolved fallback", addr, ok)
	}

	// A broken reference store degrades to "unknown" as well (never a 5xx).
	broken := newFakeRegions()
	broken.err = fmt.Errorf("master unavailable")
	g3 := newGeocoder(Settings{}, broken, nil)
	if addr, ok := g3.Reverse(context.Background(), -6.1754, 106.8272); ok || !addr.Empty() {
		t.Fatalf("broken reference = %+v ok=%v, want the unresolved fallback", addr, ok)
	}
}
