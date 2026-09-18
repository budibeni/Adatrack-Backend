package controllers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// TestVehicleFuelHistoryParsesRangeAndPagination: FR-7.7 window + pagination are
// forwarded to the store and echoed in the response envelope.
func TestVehicleFuelHistoryParsesRangeAndPagination(t *testing.T) {
	store := newFakeStore()
	store.fuelHistoryN = 42
	store.fuelLogs = []models.FuelLog{{Timestamp: "2026-09-16T10:00:00Z", FuelLevel: f64(50)}}

	svc := newTestService(store)
	target := "/api/v1/vehicles/4/fuel/history?from=2026-09-01T00:00:00Z&to=2026-09-16T00:00:00Z&page=2&limit=10"
	c, rec := testContext(http.MethodGet, target, "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "4"}}

	svc.handleVehicleFuelHistory(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	wantFrom, _ := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	wantTo, _ := time.Parse(time.RFC3339, "2026-09-16T00:00:00Z")
	if !store.fuelFrom.Equal(wantFrom) || !store.fuelTo.Equal(wantTo) {
		t.Errorf("range = %s..%s, want %s..%s", store.fuelFrom, store.fuelTo, wantFrom, wantTo)
	}
	if store.fuelPage != 2 || store.fuelLimit != 10 {
		t.Errorf("pagination = %d/%d, want 2/10", store.fuelPage, store.fuelLimit)
	}
	var env struct {
		Data       []models.FuelLog   `json:"data"`
		Pagination *models.Pagination `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode history envelope: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].FuelLevel == nil || *env.Data[0].FuelLevel != 50 {
		t.Errorf("history rows = %+v, want one 50%% sample", env.Data)
	}
	if env.Pagination == nil || env.Pagination.Total != 42 || env.Pagination.Page != 2 {
		t.Errorf("pagination = %+v, want page 2 of 42", env.Pagination)
	}
}

// TestVehicleFuelHistoryRejectsBadRange: a malformed from/to is 400 with the
// offending field named (PRD §8.5).
func TestVehicleFuelHistoryRejectsBadRange(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/4/fuel/history?from=yesterday",
		"", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "4"}}

	svc.handleVehicleFuelHistory(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	var env models.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.ErrorCode != CodeValidationError {
		t.Errorf("error_code = %q, want %q", env.ErrorCode, CodeValidationError)
	}
	if _, ok := env.Errors["from"]; !ok {
		t.Errorf("errors must mention from, got %v", env.Errors)
	}
}
