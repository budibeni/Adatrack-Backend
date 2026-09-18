package controllers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// TestUpdateFuelConfigMerges: PATCH replaces the thresholds and honours the
// optional flags (enabled=false, require_acc).
func TestUpdateFuelConfigMerges(t *testing.T) {
	store := newFakeStore()
	store.seedFuelConfig(&models.FuelConfig{ID: 6, DropThresholdPct: 10, RefuelThresholdPct: 15,
		WindowSeconds: 600, Severity: "critical", Enabled: true, CreatedAt: "2026-01-01T00:00:00Z"})

	svc := newTestService(store)
	body := `{"drop_threshold_percent":20,"refuel_threshold_percent":25,` +
		`"window_seconds":1200,"alert_severity":"high","enabled":false,"require_acc":true}`
	c, rec := testContext(http.MethodPatch, "/api/v1/fuel-configs/6", body, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "6"}}

	svc.handleUpdateFuelConfig(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	fc := decodeFuelConfig(t, rec.Body.Bytes())
	if fc.DropThresholdPct != 20 || fc.RefuelThresholdPct != 25 {
		t.Errorf("thresholds = %d/%d, want 20/25", fc.DropThresholdPct, fc.RefuelThresholdPct)
	}
	if fc.WindowSeconds != 1200 || fc.Severity != "high" {
		t.Errorf("window/severity = %d/%q, want 1200/high", fc.WindowSeconds, fc.Severity)
	}
	if fc.Enabled {
		t.Error("enabled must follow the request body (false)")
	}
	if !fc.RequireACC {
		t.Error("require_acc must follow the request body (true)")
	}
	if !store.updatedFuel {
		t.Error("the update must reach the store")
	}
}

// TestUpdateFuelConfigNotFound: PATCH on an unknown id is 404.
func TestUpdateFuelConfigNotFound(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)
	body := `{"drop_threshold_percent":10,"refuel_threshold_percent":15}`
	c, rec := testContext(http.MethodPatch, "/api/v1/fuel-configs/404", body, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "404"}}

	svc.handleUpdateFuelConfig(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeFuelConfigNotFound {
		t.Errorf("error_code = %q, want %q", code, CodeFuelConfigNotFound)
	}
	if store.updatedFuel {
		t.Error("no update may reach the store for a missing config")
	}
}
