package controllers

import (
	"encoding/json"
	"net/http"
	"testing"

	"adatrack_gps/api-vehicle/models"
)

// TestCreateFuelConfigValidation: missing thresholds are a 400 VALIDATION_ERROR
// with per-field errors (PRD §8.5).
func TestCreateFuelConfigValidation(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs",
		`{"window_seconds":600}`, adminIdentity())

	svc.handleCreateFuelConfig(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
	var env models.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.ErrorCode != CodeValidationError {
		t.Errorf("error_code = %q, want %q", env.ErrorCode, CodeValidationError)
	}
	if len(env.Errors) == 0 {
		t.Errorf("errors must name the missing thresholds, got %v", env.Errors)
	}
	if store.createdFuel {
		t.Error("the store must not be touched on a validation failure")
	}
}

// TestCreateFuelConfigVehicleMustExist: a vehicle-scoped config requires the
// vehicle row (FR-7.6) → 404 VEHICLE_NOT_FOUND when it does not exist.
func TestCreateFuelConfigVehicleMustExist(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)
	body := `{"vehicle_id":42,"drop_threshold_percent":10,"refuel_threshold_percent":15}`
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs", body, adminIdentity())

	svc.handleCreateFuelConfig(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404 (body=%s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != CodeVehicleNotFound {
		t.Errorf("error_code = %q, want %q", code, CodeVehicleNotFound)
	}
	if store.createdFuel {
		t.Error("no config may be created for a missing vehicle")
	}
}

// TestCreateFuelConfigVehicleScoped: a config bound to an existing vehicle is
// created and echoed with its vehicle_id (FR-7.6 vehicle override).
func TestCreateFuelConfigVehicleScoped(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 11, IMEI: "864201040512345", Status: "active"})
	svc := newTestService(store)
	body := `{"vehicle_id":11,"drop_threshold_percent":12,"refuel_threshold_percent":18}`
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs", body, adminIdentity())

	svc.handleCreateFuelConfig(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	fc := decodeFuelConfig(t, rec.Body.Bytes())
	if fc.VehicleID == nil || *fc.VehicleID != 11 {
		t.Errorf("vehicle_id = %v, want 11", fc.VehicleID)
	}
}

// TestCreateFuelConfigDefaults: window/severity/acc_stale/enabled defaults are
// applied server-side (FR-7.6) and the created row is echoed back.
func TestCreateFuelConfigDefaults(t *testing.T) {
	store := newFakeStore()
	svc := newTestService(store)
	body := `{"drop_threshold_percent":10,"refuel_threshold_percent":15}`
	c, rec := testContext(http.MethodPost, "/api/v1/fuel-configs", body, adminIdentity())

	svc.handleCreateFuelConfig(c)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	fc := decodeFuelConfig(t, rec.Body.Bytes())
	if fc.WindowSeconds != 600 {
		t.Errorf("window_seconds = %d, want the 600s default", fc.WindowSeconds)
	}
	if fc.Severity != "critical" {
		t.Errorf("severity = %q, want critical", fc.Severity)
	}
	if fc.ACCStaleSeconds != 600 {
		t.Errorf("acc_stale_seconds = %d, want 600", fc.ACCStaleSeconds)
	}
	if !fc.Enabled {
		t.Error("a new config must default to enabled")
	}
	if fc.VehicleID != nil {
		t.Errorf("vehicle_id = %v, want the tenant-wide default (null)", fc.VehicleID)
	}
	if !store.createdFuel {
		t.Error("the config must be persisted")
	}
}
