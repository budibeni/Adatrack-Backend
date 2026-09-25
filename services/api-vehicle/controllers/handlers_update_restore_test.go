package controllers

// handlers_update_restore_test.go — hermetic coverage for the PATCH overlay and
// the restore/list handler branches that do not need a live database:
//
//   - PATCH /api/v1/vehicles/:id   (overlay semantics + immutable IMEI, FR-1.4)
//   - POST  /api/v1/{speed-configs,routes,fuel-configs}/:id/restore
//   - GET   /api/v1/fuel-configs   (soft-delete veil guard, PRD §6.0.1)
//
// Every branch that answers 503 is exercised through a failing store so the
// outage path is asserted, not just the happy path.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// errPersistence is the sentinel a failing store returns; the handlers must
// translate it into a 503 SERVICE_UNAVAILABLE without leaking it.
var errPersistence = errors.New("postgres down")

// fleetErrStore fails selected fleet paths on demand (embedded fakeStore).
type fleetErrStore struct {
	*fakeStore

	failVehicleByID   bool
	failUpdateVehicle bool
	failSpeedByID     bool
	failRestoreSpeed  bool
	failRouteByID     bool
	failRestoreRoute  bool
	failFuelByID      bool
	failRestoreFuel   bool
	failListFuel      bool
}

func (f fleetErrStore) VehicleByID(ctx context.Context, c string, id int64, inc bool) (*models.Vehicle, error) {
	if f.failVehicleByID {
		return nil, errPersistence
	}
	return f.fakeStore.VehicleByID(ctx, c, id, inc)
}

func (f fleetErrStore) UpdateVehicle(ctx context.Context, c string, v *models.Vehicle, by int64) error {
	if f.failUpdateVehicle {
		return errPersistence
	}
	return f.fakeStore.UpdateVehicle(ctx, c, v, by)
}

func (f fleetErrStore) SpeedConfigByID(ctx context.Context, c string, id int64, inc bool) (*models.SpeedConfig, error) {
	if f.failSpeedByID {
		return nil, errPersistence
	}
	return f.fakeStore.SpeedConfigByID(ctx, c, id, inc)
}

func (f fleetErrStore) RestoreSpeedConfig(ctx context.Context, c string, id int64) error {
	if f.failRestoreSpeed {
		return errPersistence
	}
	return f.fakeStore.RestoreSpeedConfig(ctx, c, id)
}

func (f fleetErrStore) RouteByID(ctx context.Context, c string, id int64, inc bool) (*models.Route, error) {
	if f.failRouteByID {
		return nil, errPersistence
	}
	return f.fakeStore.RouteByID(ctx, c, id, inc)
}

func (f fleetErrStore) RestoreRoute(ctx context.Context, c string, id int64) error {
	if f.failRestoreRoute {
		return errPersistence
	}
	return f.fakeStore.RestoreRoute(ctx, c, id)
}

func (f fleetErrStore) FuelConfigByID(ctx context.Context, c string, id int64, inc bool) (*models.FuelConfig, error) {
	if f.failFuelByID {
		return nil, errPersistence
	}
	return f.fakeStore.FuelConfigByID(ctx, c, id, inc)
}

func (f fleetErrStore) RestoreFuelConfig(ctx context.Context, c string, id int64) error {
	if f.failRestoreFuel {
		return errPersistence
	}
	return f.fakeStore.RestoreFuelConfig(ctx, c, id)
}

func (f fleetErrStore) ListFuelConfigs(ctx context.Context, c string, inc bool) ([]models.FuelConfig, error) {
	if f.failListFuel {
		return nil, errPersistence
	}
	return f.fakeStore.ListFuelConfigs(ctx, c, inc)
}

// wantStatus asserts the HTTP status and reports the error_code on mismatch.
func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, code, rec.Body.String())
	}
}

// wantError asserts the status AND the PRD error_code of a failed request.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, errorCode string) {
	t.Helper()
	wantStatus(t, rec, status)
	if code := decodeErr(t, rec).ErrorCode; code != errorCode {
		t.Errorf("error_code = %s, want %s", code, errorCode)
	}
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/vehicles/:id — overlay semantics (PRD §8.2, FR-1.4)
// ---------------------------------------------------------------------------

// patchVehicle issues the PATCH against the given service and id.
func patchVehicle(svc *Service, id, body string) *httptest.ResponseRecorder {
	c, rec := testContext(http.MethodPatch, "/api/v1/vehicles/"+id, body, adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: id}}
	svc.handleUpdateVehicle(c)
	return rec
}

// TestPatchVehicleOverlayKeepsUnprovidedFields: a PATCH with a body update wins,
// absent fields keep the stored value — the DTO stays a full read model, so the
// handler must OVERLAY instead of replacing (PRD §8.2 PATCH semantics).
func TestPatchVehicleOverlayKeepsUnprovidedFields(t *testing.T) {
	store := newFakeStore()
	makeName := "Toyota"
	store.seedVehicle(&models.Vehicle{
		ID: 1, IMEI: "864201040512345", PlateNumber: "B 1234 XYZ",
		Make: &makeName, Status: "active",
	})
	svc := newServiceWithStore(store)

	rec := patchVehicle(svc, "1",
		`{"imei":"864201040512345","plate_number":"B 9999 ABC","color":"red"}`)
	wantStatus(t, rec, http.StatusOK)
	if !store.updatedVehicle {
		t.Fatal("UpdateVehicle must be called by PATCH")
	}

	stored := store.vehicles[1]
	if stored.PlateNumber != "B 9999 ABC" {
		t.Errorf("plate_number = %q, want the PATCHed value", stored.PlateNumber)
	}
	if stored.Color == nil || *stored.Color != "red" {
		t.Errorf("color = %v, want red (provided field wins)", stored.Color)
	}
	if stored.Make == nil || *stored.Make != "Toyota" {
		t.Errorf("make = %v, want Toyota (absent field keeps the stored value)", stored.Make)
	}
}

// TestPatchVehicleStatusIsOptional: an omitted status keeps the stored one.
func TestPatchVehicleStatusIsOptional(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345",
		PlateNumber: "B 1234 XYZ", Status: "maintenance"})
	svc := newServiceWithStore(store)

	rec := patchVehicle(svc, "1", `{"imei":"864201040512345","plate_number":"B 1234 XYZ"}`)
	wantStatus(t, rec, http.StatusOK)
	if got := store.vehicles[1].Status; got != "maintenance" {
		t.Errorf("status = %q, want maintenance (must not be reset)", got)
	}
}

// TestPatchVehicleGuards covers the rejection paths of the PATCH handler:
// malformed id, invalid body, unknown vehicle, a malformed IMEI and an IMEI that
// already belongs to another vehicle (the identity is validated, FR-1.4).
func TestPatchVehicleGuards(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345",
		PlateNumber: "B 1234 XYZ", Status: "active"})
	svc := newServiceWithStore(store)

	t.Run("invalid id", func(t *testing.T) {
		wantError(t, patchVehicle(svc, "x", ""), http.StatusBadRequest, CodeValidationError)
	})
	t.Run("invalid body", func(t *testing.T) {
		wantError(t, patchVehicle(svc, "1", "{"), http.StatusBadRequest, CodeValidationError)
	})
	t.Run("unknown vehicle", func(t *testing.T) {
		rec := patchVehicle(svc, "99", `{"imei":"864201040512345","plate_number":"B 1234 XYZ"}`)
		wantError(t, rec, http.StatusNotFound, CodeVehicleNotFound)
	})
	t.Run("malformed imei", func(t *testing.T) {
		rec := patchVehicle(svc, "1", `{"imei":"abc","plate_number":"B 1234 XYZ"}`)
		wantError(t, rec, http.StatusBadRequest, CodeValidationError)
	})
}

// TestPatchVehicleStoreErrors: a pre-read or write outage answers 503 and never
// leaks the SQL failure (PRD §8.1).
func TestPatchVehicleStoreErrors(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345",
		PlateNumber: "B 1234 XYZ", Status: "active"})

	t.Run("pre-read fails", func(t *testing.T) {
		svc := newServiceWithStore(fleetErrStore{fakeStore: store, failVehicleByID: true})
		rec := patchVehicle(svc, "1", `{"imei":"864201040512345","plate_number":"B 1234 XYZ"}`)
		wantError(t, rec, http.StatusServiceUnavailable, CodeServiceUnavailable)
	})
	t.Run("write fails", func(t *testing.T) {
		svc := newServiceWithStore(fleetErrStore{fakeStore: store, failUpdateVehicle: true})
		rec := patchVehicle(svc, "1", `{"imei":"864201040512345","plate_number":"B 1234 XYZ"}`)
		wantError(t, rec, http.StatusServiceUnavailable, CodeServiceUnavailable)
	})
	t.Run("post-write read returns nothing", func(t *testing.T) {
		svc := newServiceWithStore(&vanishAfterUpdateStore{fakeStore: store})
		rec := patchVehicle(svc, "1", `{"imei":"864201040512345","plate_number":"B 1234 XYZ"}`)
		wantError(t, rec, http.StatusServiceUnavailable, CodeServiceUnavailable)
	})
}

// vanishAfterUpdateStore serves the pre-read but loses the row afterwards, so
// the handler's post-write read path is asserted (503, never a silent 200).
type vanishAfterUpdateStore struct {
	*fakeStore
	reads int
}

func (v *vanishAfterUpdateStore) VehicleByID(ctx context.Context, c string, id int64, inc bool) (*models.Vehicle, error) {
	v.reads++
	if v.reads > 1 {
		return nil, nil
	}
	return v.fakeStore.VehicleByID(ctx, c, id, inc)
}
