package controllers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// --- fuel-config response helpers (shared by the *_test.go files) ----------

// decodeFuelConfig decodes the `data` block of a fuel-config envelope.
func decodeFuelConfig(t *testing.T, raw []byte) models.FuelConfig {
	t.Helper()
	var env struct {
		Data models.FuelConfig `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode fuel config envelope: %v (body=%s)", err, raw)
	}
	return env.Data
}

// decodeFuelConfigs decodes a fuel-config list envelope.
func decodeFuelConfigs(t *testing.T, raw []byte) []models.FuelConfig {
	t.Helper()
	var env struct {
		Data []models.FuelConfig `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode fuel config list: %v (body=%s)", err, raw)
	}
	return env.Data
}

// decodeErrorCode returns the PRD §8.1 error_code of a failure response.
func decodeErrorCode(t *testing.T, raw []byte) string {
	t.Helper()
	var env models.ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode error envelope: %v (body=%s)", err, raw)
	}
	return env.ErrorCode
}

// f64 is a test helper that returns a *float64.
func f64(v float64) *float64 { return &v }

// --- list/detail (not duplicated across the specialized test files) -------

// TestListFuelConfigsReturnsRows: GET /fuel-configs returns the tenant configs
// (vehicle-specific + tenant-wide default, FR-7.6).
func TestListFuelConfigsReturnsRows(t *testing.T) {
	store := newFakeStore()
	vid := int64(3)
	store.seedFuelConfig(&models.FuelConfig{ID: 1, VehicleID: &vid, DropThresholdPct: 10,
		RefuelThresholdPct: 15, WindowSeconds: 600, Severity: "critical", Enabled: true})
	store.seedFuelConfig(&models.FuelConfig{ID: 2, DropThresholdPct: 8,
		RefuelThresholdPct: 12, WindowSeconds: 900, Severity: "high", Enabled: true})

	svc := newTestService(store)
	c, rec := testContext(http.MethodGet, "/api/v1/fuel-configs", "", adminIdentity())

	svc.handleListFuelConfigs(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	items := decodeFuelConfigs(t, rec.Body.Bytes())
	if len(items) != 2 {
		t.Fatalf("rows = %d, want 2", len(items))
	}
	if items[0].VehicleID == nil || *items[0].VehicleID != 3 {
		t.Errorf("row 0 vehicle_id = %v, want 3", items[0].VehicleID)
	}
	if items[1].VehicleID != nil {
		t.Error("row 1 must be the tenant-wide default (vehicle_id null)")
	}
}

// TestFuelConfigDetailNotFound: an unknown id is 404 FUEL_CONFIG_NOT_FOUND.
func TestFuelConfigDetailNotFound(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodGet, "/api/v1/fuel-configs/99", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "99"}}

	svc.handleFuelConfigDetail(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeFuelConfigNotFound {
		t.Errorf("error_code = %q, want %q", code, CodeFuelConfigNotFound)
	}
}

// TestFuelConfigDetailReturnsRow: a known id returns the config.
func TestFuelConfigDetailReturnsRow(t *testing.T) {
	store := newFakeStore()
	store.seedFuelConfig(&models.FuelConfig{ID: 5, DropThresholdPct: 7, RefuelThresholdPct: 9,
		WindowSeconds: 300, Severity: "medium", Enabled: true})

	svc := newTestService(store)
	c, rec := testContext(http.MethodGet, "/api/v1/fuel-configs/5", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "5"}}

	svc.handleFuelConfigDetail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	fc := decodeFuelConfig(t, rec.Body.Bytes())
	if fc.ID != 5 || fc.DropThresholdPct != 7 || fc.WindowSeconds != 300 {
		t.Errorf("unexpected config: %+v", fc)
	}
}
