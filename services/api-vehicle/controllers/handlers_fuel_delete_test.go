package controllers

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// TestDeleteFuelConfigSoftDeletes: DELETE hides the config from the default
// list/detail but keeps it visible (and restorable) with include_deleted=true
// (PRD §6.0.1).
func TestDeleteFuelConfigSoftDeletes(t *testing.T) {
	store := newFakeStore()
	store.seedFuelConfig(&models.FuelConfig{ID: 7, DropThresholdPct: 10,
		RefuelThresholdPct: 15, WindowSeconds: 600, Severity: "critical", Enabled: true})

	svc := newTestService(store)

	c, rec := testContext(http.MethodDelete, "/api/v1/fuel-configs/7", `{"reason":"obsolete"}`, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	svc.handleDeleteFuelConfig(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: got %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !store.deletedFuel {
		t.Error("the soft delete must reach the store")
	}

	// Hidden from the default detail read.
	c2, rec2 := testContext(http.MethodGet, "/api/v1/fuel-configs/7", "", adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "7"}}
	svc.handleFuelConfigDetail(c2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("deleted detail: got %d, want 404", rec2.Code)
	}

	// Still listed when the Admin asks for deleted rows.
	c3, rec3 := testContext(http.MethodGet, "/api/v1/fuel-configs?include_deleted=true", "", adminIdentity())
	svc.handleListFuelConfigs(c3)
	if items := decodeFuelConfigs(t, rec3.Body.Bytes()); len(items) != 1 {
		t.Fatalf("include_deleted rows = %d, want 1", len(items))
	}

	// Restore brings it back.
	c4, rec4 := testContext(http.MethodPost, "/api/v1/fuel-configs/7/restore", "", adminIdentity())
	c4.Params = gin.Params{{Key: "id", Value: "7"}}
	svc.handleRestoreFuelConfig(c4)
	if rec4.Code != http.StatusOK {
		t.Fatalf("restore: got %d, want 200 (body=%s)", rec4.Code, rec4.Body.String())
	}
	if !store.restoredFuel {
		t.Error("the restore must reach the store")
	}
	if fc := decodeFuelConfig(t, rec4.Body.Bytes()); fc.DeletedAt != nil {
		t.Errorf("restored row is still soft deleted: %v", *fc.DeletedAt)
	}
}

// TestRestoreFuelConfigNotDeleted: restoring a live config is a client error,
// not a silent no-op.
func TestRestoreFuelConfigNotDeleted(t *testing.T) {
	store := newFakeStore()
	store.seedFuelConfig(&models.FuelConfig{ID: 8, DropThresholdPct: 10,
		RefuelThresholdPct: 15, WindowSeconds: 600, Severity: "critical", Enabled: true})

	svc := newTestService(store)
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs/8/restore", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "8"}}

	svc.handleRestoreFuelConfig(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeConflict {
		t.Errorf("error_code = %q, want %q", code, CodeConflict)
	}
	if store.restoredFuel {
		t.Error("the store must not be asked to restore a live row")
	}
}

// TestRestoreFuelConfigNotFound: restoring an unknown id is 404.
func TestRestoreFuelConfigNotFound(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs/77/restore", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "77"}}

	svc.handleRestoreFuelConfig(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeFuelConfigNotFound {
		t.Errorf("error_code = %q, want %q", code, CodeFuelConfigNotFound)
	}
}
