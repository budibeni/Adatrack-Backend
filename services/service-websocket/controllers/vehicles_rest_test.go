package controllers

import (
	"net/http"
	"testing"

	"ajb_gps/service-websocket/models"
)

// TestVehicleListContract asserts the PRD §8.1 envelope + §8.2 pagination.
func TestVehicleListContract(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles?page=1&limit=2", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if body["status"] != "success" {
		t.Fatalf("status field = %v, want success", body["status"])
	}
	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("pagination block missing: %v", body)
	}
	if pagination["page"].(float64) != 1 || pagination["limit"].(float64) != 2 || pagination["total"].(float64) != 3 {
		t.Fatalf("pagination = %v, want page=1 limit=2 total=3", pagination)
	}
	if len(body["data"].([]any)) != 2 {
		t.Fatalf("page size = %d, want 2", len(body["data"].([]any)))
	}

	// page 2 returns the remaining row.
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles?page=2&limit=2", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page 2 status = %d, want 200", resp.StatusCode)
	}
	if len(body["data"].([]any)) != 1 {
		t.Fatalf("page 2 size = %d, want 1", len(body["data"].([]any)))
	}
}

// TestVehicleListPaginationValidation covers PRD §8.5 rule 4 (bounded paging).
func TestVehicleListPaginationValidation(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	for _, query := range []string{"?page=0", "?page=abc", "?limit=0", "?limit=5000"} {
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles"+query, access, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %v)", query, resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("%s error_code = %q, want %s", query, got, CodeValidationError)
		}
	}
}

// TestVehicleStatusFilterValidation asserts the enum whitelist (PRD §8.5 rule 2).
func TestVehicleStatusFilterValidation(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles?status=exploded", access, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
	}

	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles?status=maintenance", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if len(body["data"].([]any)) != 1 {
		t.Fatalf("maintenance vehicles = %d, want 1", len(body["data"].([]any)))
	}
}

// TestVehicleLiveEnrichment asserts FR-5.1: position/speed/acc/fuel/satellites/
// altitude/gsm_signal are merged from the Redis live state.
func TestVehicleLiveEnrichment(t *testing.T) {
	h := newHarness(t)
	acc := true
	fuelLevel := 61.5
	h.live.set("864201040512345", models.LiveState{
		CompanyCode: "DEV001", VehicleID: 1, Lat: -6.2088, Lon: 106.8456, Speed: 45.2,
		Heading: 180, Satellites: 9, Altitude: 112, GsmSignal: 4, Battery: 85,
		ACC: &acc, Status: models.StatusOnline, FuelLevel: &fuelLevel,
	})

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	vehicle := body["data"].(map[string]any)
	live, ok := vehicle["live"].(map[string]any)
	if !ok {
		t.Fatalf("live block missing: %v", vehicle)
	}
	if live["speed"].(float64) != 45.2 {
		t.Fatalf("live.speed = %v, want 45.2", live["speed"])
	}
	if live["acc"].(bool) != true {
		t.Fatalf("live.acc = %v, want true (real tracker value, not speed>0)", live["acc"])
	}
	if live["fuel_level"].(float64) != 61.5 {
		t.Fatalf("live.fuel_level = %v, want 61.5", live["fuel_level"])
	}
	if live["satellites"].(float64) != 9 || live["altitude"].(float64) != 112 || live["gsm_signal"].(float64) != 4 {
		t.Fatalf("B6 enrichment missing: %v", live)
	}
}

// TestVehicleHistoryContract asserts the history endpoint pagination + range
// validation (PRD §8.2/§8.5).
func TestVehicleHistoryContract(t *testing.T) {
	h := newHarness(t)
	h.store.positions["864201040512345"] = []models.Position{
		{Timestamp: "2026-09-15T10:00:00Z", Lat: -6.2, Lon: 106.8, Speed: 10},
		{Timestamp: "2026-09-15T10:00:20Z", Lat: -6.21, Lon: 106.81, Speed: 20},
		{Timestamp: "2026-09-15T10:00:40Z", Lat: -6.22, Lon: 106.82, Speed: 30},
	}
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1/history?page=1&limit=2", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	pagination := body["pagination"].(map[string]any)
	if pagination["total"].(float64) != 3 {
		t.Fatalf("history total = %v, want 3", pagination["total"])
	}
	if len(body["data"].([]any)) != 2 {
		t.Fatalf("history page size = %d, want 2", len(body["data"].([]any)))
	}
}
