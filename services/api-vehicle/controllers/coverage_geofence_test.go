package controllers

// coverage_geofence_test.go (B4 2026-09-08): geofence<->vehicle link handlers.

import (
	"net/http"
	"testing"

	"ajb_gps/internal"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	gin.SetMode(gin.TestMode)
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

// ---------------------------------------------------------------------
// POST /geofences/:id/vehicles — geofenceVehiclesAddHandler
// ---------------------------------------------------------------------

func TestGeofenceAddSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM geofences WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT IGNORE INTO geofence_vehicles \\(geofence_id, vehicle_id, is_enabled\\) VALUES \\(\\?, \\?, TRUE\\)").
		WithArgs(uint64(5), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))

	geofenceVehiclesAddHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("geofence add = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGeofenceAddNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/geofences/404/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM geofences WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	geofenceVehiclesAddHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("geofence add 404 = %d, want 404", code)
	}
}

func TestGeofenceAddOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")

	geofenceVehiclesAddHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("geofence add operator = %d, want 403", code)
	}
}

// ---------------------------------------------------------------------
// DELETE /geofences/:id/vehicles/:vehicleId — geofenceVehiclesRemoveHandler
// ---------------------------------------------------------------------

func TestGeofenceRemoveSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/geofences/5/vehicles/9", "")
	// Override auto-extracted params: helper sets id=9 (trailing numeric), but
	// geofence handler needs id=5 (geofence) + vehicleId=9.
	c.Params = gin.Params{{Key: "id", Value: "5"}, {Key: "vehicleId", Value: "9"}}

	m.ExpectExec("(?i)DELETE FROM geofence_vehicles WHERE geofence_id = \\? AND vehicle_id = \\?").
		WithArgs(uint64(5), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 1))

	geofenceVehiclesRemoveHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("geofence remove = %d, want 200", code)
	}
}

func TestGeofenceRemoveNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/geofences/5/vehicles/9", "")
	c.Params = gin.Params{{Key: "id", Value: "5"}, {Key: "vehicleId", Value: "9"}}

	m.ExpectExec("(?i)DELETE FROM geofence_vehicles WHERE geofence_id = \\? AND vehicle_id = \\?").
		WithArgs(uint64(5), uint64(9)).WillReturnResult(sqlmock.NewResult(0, 0))

	geofenceVehiclesRemoveHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("geofence remove 404 = %d, want 404", code)
	}
}

func TestGeofenceRemoveBadVehicleID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/geofences/5/vehicles/abc", "")
	c.AddParam("id", "5")
	c.AddParam("vehicleId", "abc")

	geofenceVehiclesRemoveHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("geofence remove bad id = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// GET /geofences/:id/vehicles — geofenceVehiclesListHandler
// ---------------------------------------------------------------------

func TestGeofenceListVehicles(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/geofences/5/vehicles", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT gv\\.vehicle_id FROM geofence_vehicles gv WHERE gv\\.geofence_id = \\? AND gv\\.is_enabled = TRUE ORDER BY gv\\.vehicle_id").
		WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(1)).AddRow(uint64(2)))

	geofenceVehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("geofence list = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestGeofenceListNonAdminFilters(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, false, "GET", "/geofences/5/vehicles", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT gv\\.vehicle_id FROM geofence_vehicles gv WHERE gv\\.geofence_id = \\? AND gv\\.is_enabled = TRUE ORDER BY gv\\.vehicle_id").
		WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(1)).AddRow(uint64(9)))

	geofenceVehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("geofence list non-admin = %d, want 200", code)
	}
}

func TestGeofenceListBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/geofences/abc/vehicles", "")
	c.AddParam("id", "abc")

	geofenceVehiclesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("geofence list bad id = %d, want 400", code)
	}
}