package controllers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// parseJSON decodes a JSON body (small helper so tests stay terse).
func parseJSON(raw []byte, dst any) error {
	return json.Unmarshal(raw, dst)
}

// TestUpdateVehicleIMEIImmutable: PATCH cannot change the device identity.
func TestUpdateVehicleIMEIImmutable(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 5, IMEI: "864201040512345", PlateNumber: "B 1 A", Status: "active"})
	svc := newTestService(store)

	body := `{"imei":"999999999999999","plate_number":"B 2 B"}`
	c, rec := testContext(http.MethodPatch, "/api/v1/vehicles/5", body,
		adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "5"}}

	svc.handleUpdateVehicle(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409 CONFLICT for an IMEI change", rec.Code)
	}
	if store.updatedVehicle {
		t.Error("the store must not be touched when the IMEI differs")
	}
}

// TestGeofenceCreateGeometryValidation: a polygon with < 3 points is a 400 with
// a field error (PRD §5.9.1 + DB CHECK re-asserted at the API layer).
func TestGeofenceCreateGeometryValidation(t *testing.T) {
	svc := newTestService(newFakeStore())
	body := `{"name":"Yard","area_type":"polygon","boundary_points":[[0,0],[1,1]]}`
	c, rec := testContext(http.MethodPost, "/api/v1/geofences", body, adminIdentity())

	svc.handleCreateGeofence(c)

	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if rec.Code != http.StatusBadRequest || env.ErrorCode != CodeValidationError {
		t.Fatalf("got %d %s, want 400 VALIDATION_ERROR", rec.Code, env.ErrorCode)
	}
	if _, ok := env.Errors["boundary_points"]; !ok {
		t.Errorf("errors must mention boundary_points, got %v", env.Errors)
	}
}

// TestAcknowledgeAlertLifecycle: open → acknowledged succeeds; acknowledging an
// already-acknowledged alert is a CONFLICT (the TTA is written exactly once).
func TestAcknowledgeAlertLifecycle(t *testing.T) {
	store := newFakeStore()
	store.seedAlert(&models.Alert{ID: 11, Type: "sos", Status: "open"})
	svc := newTestService(store)

	c, rec := testContext(http.MethodPost, "/api/v1/alerts/11/acknowledge", "",
		adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "11"}}
	svc.handleAcknowledgeAlert(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("first acknowledge got %d, want 200", rec.Code)
	}

	c2, rec2 := testContext(http.MethodPost, "/api/v1/alerts/11/acknowledge", "",
		adminIdentity())
	c2.Params = gin.Params{{Key: "id", Value: "11"}}
	svc.handleAcknowledgeAlert(c2)
	var env models.ErrorEnvelope
	if err := parseJSON(rec2.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if rec2.Code != http.StatusBadRequest || env.ErrorCode != CodeConflict {
		t.Errorf("second acknowledge got %d %s, want 400 CONFLICT", rec2.Code, env.ErrorCode)
	}
}

// TestListVehiclesPaginationGuard: limit above API_MAX_PAGE_SIZE is a 400.
func TestListVehiclesPaginationGuard(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles?page=1&limit=5000", "",
		adminIdentity())

	svc.handleListVehicles(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 for an oversized limit", rec.Code)
	}
}

// TestListVehiclesIncludeDeletedAdminOnly: only Admin may look behind the veil.
func TestListVehiclesIncludeDeletedAdminOnly(t *testing.T) {
	svc := newTestService(newFakeStore())
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles?include_deleted=true", "",
		operatorIdentity(2))

	svc.handleListVehicles(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403 for a non-Admin include_deleted", rec.Code)
	}
}

// TestCreateVehicleConflictAndSuccess: duplicate IMEI → 409; fresh IMEI → 201.
func TestCreateVehicleConflictAndSuccess(t *testing.T) {
	store := newFakeStore()
	store.imeiExists = true
	svc := newTestService(store)

	body := `{"imei":"864201040512345","plate_number":"B 1 A"}`
	c, rec := testContext(http.MethodPost, "/api/v1/vehicles", body, adminIdentity())
	svc.handleCreateVehicle(c)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate IMEI got %d, want 409", rec.Code)
	}

	store.imeiExists = false
	c2, rec2 := testContext(http.MethodPost, "/api/v1/vehicles", body, adminIdentity())
	svc.handleCreateVehicle(c2)
	if rec2.Code != http.StatusCreated || !store.createdVehicle {
		t.Fatalf("fresh IMEI got %d, want 201 (created=%v)", rec2.Code, store.createdVehicle)
	}
}
