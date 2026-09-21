package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"adatrack_gps/api-vehicle/models"
)

// vehicleErrStore makes the vehicle write paths fail so the handlers' 503
// degradation is exercised (a persistence outage must never leak internals).
type vehicleErrStore struct{ *fakeStore }

func (vehicleErrStore) SoftDeleteVehicle(context.Context, string, int64, int64, string) error {
	return errors.New("postgres down")
}

func (vehicleErrStore) RestoreVehicle(context.Context, string, int64) error {
	return errors.New("postgres down")
}

func (vehicleErrStore) VehicleByID(context.Context, string, int64, bool) (*models.Vehicle, error) {
	return nil, errors.New("postgres down")
}

// deletedVehicle is a vehicle carrying the soft-delete veil.
func deletedVehicle(id int64) *models.Vehicle {
	at := "2026-09-16T00:00:00Z"
	return &models.Vehicle{ID: id, IMEI: "864201040512345", PlateNumber: "B 1234 XYZ", DeletedAt: &at}
}

// TestDeleteVehicleGuardsAndSuccess covers DELETE /api/v1/vehicles/:id: the path
// guard, the default delete reason and the soft-delete call (PRD §6.0.1).
func TestDeleteVehicleGuardsAndSuccess(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345", PlateNumber: "B 1234 XYZ"})
	svc := newServiceWithStore(store)

	t.Run("invalid id", func(t *testing.T) {
		c, rec := testContext(http.MethodDelete, "/api/v1/vehicles/x", "", adminIdentity())
		c.Params = gin.Params{{Key: "id", Value: "x"}}
		svc.handleDeleteVehicle(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("got %d, want 400", rec.Code)
		}
		if code := decodeErr(t, rec).ErrorCode; code != CodeValidationError {
			t.Errorf("error_code = %s, want %s", code, CodeValidationError)
		}
	})

	t.Run("success with explicit reason", func(t *testing.T) {
		c, rec := testContext(http.MethodDelete, "/api/v1/vehicles/1", `{"reason":"sold"}`, adminIdentity())
		c.Params = gin.Params{{Key: "id", Value: "1"}}
		svc.handleDeleteVehicle(c)

		if rec.Code != http.StatusOK || !store.deletedVehicle {
			t.Fatalf("got %d (deleted=%v), want 200", rec.Code, store.deletedVehicle)
		}
		if store.vehicles[1].DeletedAt == nil {
			t.Error("the row must be soft-deleted in the store")
		}
	})
}

// TestDeleteVehicleWithoutBodyUsesDefaultReason: the body is optional.
func TestDeleteVehicleWithoutBodyUsesDefaultReason(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345"})
	svc := newServiceWithStore(store)

	c, rec := testContext(http.MethodDelete, "/api/v1/vehicles/1", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteVehicle(c)

	if rec.Code != http.StatusOK || !store.deletedVehicle {
		t.Fatalf("got %d (deleted=%v), want 200", rec.Code, store.deletedVehicle)
	}
}

// TestDeleteVehicleStoreError answers 503 instead of leaking the SQL failure.
func TestDeleteVehicleStoreError(t *testing.T) {
	svc := newServiceWithStore(vehicleErrStore{newFakeStore()})

	c, rec := testContext(http.MethodDelete, "/api/v1/vehicles/1", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	svc.handleDeleteVehicle(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
	if code := decodeErr(t, rec).ErrorCode; code != CodeServiceUnavailable {
		t.Errorf("error_code = %s, want %s", code, CodeServiceUnavailable)
	}
}

// TestRestoreVehicleLifecycle covers POST /api/v1/vehicles/:id/restore: an
// invalid id (400), an unknown vehicle (404), a row that is not deleted (400
// CONFLICT) and the successful restore.
func TestRestoreVehicleLifecycle(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(&models.Vehicle{ID: 1, IMEI: "864201040512345"})
	store.seedVehicle(deletedVehicle(2))
	svc := newServiceWithStore(store)

	restore := func(id string) *httptest.ResponseRecorder {
		c, rec := testContext(http.MethodPost, "/api/v1/vehicles/"+id+"/restore", "", adminIdentity())
		c.Params = gin.Params{{Key: "id", Value: id}}
		svc.handleRestoreVehicle(c)
		return rec
	}

	if rec := restore("x"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid id = %d, want 400", rec.Code)
	}
	if rec := restore("99"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown vehicle = %d, want 404", rec.Code)
	}
	if rec := restore("1"); rec.Code != http.StatusBadRequest {
		t.Errorf("restoring a live vehicle = %d, want 400 CONFLICT", rec.Code)
	} else if code := decodeErr(t, rec).ErrorCode; code != CodeConflict {
		t.Errorf("error_code = %s, want %s", code, CodeConflict)
	}

	rec := restore("2")
	if rec.Code != http.StatusOK || !store.restoredVehicle {
		t.Fatalf("restore = %d (restored=%v), want 200", rec.Code, store.restoredVehicle)
	}
	if store.vehicles[2].DeletedAt != nil {
		t.Error("deleted_at must be cleared by the restore")
	}
}

// TestRestoreVehicleStoreError answers 503 on a persistence failure.
func TestRestoreVehicleStoreError(t *testing.T) {
	svc := newServiceWithStore(vehicleErrStore{newFakeStore()})

	c, rec := testContext(http.MethodPost, "/api/v1/vehicles/2/restore", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "2"}}
	svc.handleRestoreVehicle(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

// TestRestoreVehicleMissingAfterRestore: the post-restore read must still find
// the row; a store that loses it surfaces as 503 rather than a silent 200.
func TestRestoreVehicleMissingAfterRestore(t *testing.T) {
	store := newFakeStore()
	store.seedVehicle(deletedVehicle(2))
	svc := newServiceWithStore(vanishingStore{store})

	c, rec := testContext(http.MethodPost, "/api/v1/vehicles/2/restore", "", adminIdentity())
	c.Params = gin.Params{{Key: "id", Value: "2"}}
	svc.handleRestoreVehicle(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rec.Code)
	}
}

// vanishingStore restores a row but then reports it as absent, simulating a
// replication/lost-write accident.
type vanishingStore struct{ *fakeStore }

func (v vanishingStore) VehicleByID(_ context.Context, _ string, id int64, includeDeleted bool) (*models.Vehicle, error) {
	if includeDeleted {
		return v.fakeStore.VehicleByID(context.Background(), "", id, true)
	}
	return nil, nil
}
