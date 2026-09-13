package controllers

// b4_coverage13_test.go (B4 coverage 2026-09-05 — Stage J):
// branch tests for vehicleHistoryHandler validation guards,
// geofenceDetailHandler RBAC (operator granted/denied), routeTrackHandler BadID.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// vehicleHistoryHandler — validation guards (loadVehicleByID must succeed first)
// ---------------------------------------------------------------------------

var vehicleHistoryLoadCols = []string{
	"id", "imei", "plate_number", "device_model", "status",
	"last_seen_at", "current_latitude", "current_longitude", "current_speed",
}

func vehicleHistoryLoadRow() *sqlmock.Rows {
	return sqlmock.NewRows(vehicleHistoryLoadCols).AddRow(
		uint64(42), "864000000041234", "B 1234 XYZ",
		sql.NullString{String: "GT06", Valid: true}, "MOVING",
		time.Now(), sql.NullFloat64{Float64: -6.2, Valid: true},
		sql.NullFloat64{Float64: 106.8, Valid: true},
		sql.NullFloat64{Float64: 35.5, Valid: true})
}

func expectVehicleHistoryLoad(m sqlmock.Sqlmock) {
	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).WithArgs(uint64(42)).
		WillReturnRows(vehicleHistoryLoadRow())
}

// ---------------------------------------------------------------------------
// vehicleHistoryHandler — validation guard tests (each ends in 400 before SQL)
// ---------------------------------------------------------------------------

func TestVehicleHistoryHandler_InvalidStart(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start=not-a-date&end=2026-08-02T00:00:00Z",
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	expectVehicleHistoryLoad(m)

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_InvalidEnd(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start=2026-08-01T00:00:00Z&end=garbage",
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	expectVehicleHistoryLoad(m)

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_EndBeforeStart(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start=2026-08-02T00:00:00Z&end=2026-08-01T00:00:00Z",
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	expectVehicleHistoryLoad(m)

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_WindowTooLong(t *testing.T) {
	db, m := mockDB(t)
	start := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	end := time.Now().UTC().Format(time.RFC3339)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start="+start+"&end="+end,
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	expectVehicleHistoryLoad(m)

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_InvalidLimit(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start=2026-08-01T00:00:00Z&end=2026-08-02T00:00:00Z&limit=999999",
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	expectVehicleHistoryLoad(m)

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_BadID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles/abc/history", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// geofenceDetailHandler — RBAC paths
// ---------------------------------------------------------------------------

func geofenceRow() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points",
		"created_by", "is_active", "created_at",
	}).AddRow(uint64(1), "GF-A", "circle", []byte(`{"lat":-6.2,"lon":106.8}`),
		sql.NullFloat64{Float64: 100, Valid: true}, nil, uint64(1), true, time.Now())
}

func TestGeofenceDetailHandler_OperatorNoAccess(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences/1", db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, area_type`).WithArgs(uint64(1)).
		WillReturnRows(geofenceRow())
	// geofenceAccessible → COUNT = 0 → denied.
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM geofence_vehicles`).
		WithArgs(uint64(1), uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(0)))

	geofenceDetailHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofenceDetailHandler_OperatorWithAccess(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences/1", db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, area_type`).WithArgs(uint64(1)).
		WillReturnRows(geofenceRow())
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM geofence_vehicles`).
		WithArgs(uint64(1), uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	// toItem → load linked vehicles.
	m.ExpectQuery(`SELECT vehicle_id FROM geofence_vehicles`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(42)))

	geofenceDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "GF-A") {
		t.Errorf("missing name: %s", rec.Body.String())
	}
}

func TestGeofenceDetailHandler_BadID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences/xyz", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "xyz"}}

	geofenceDetailHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routeTrackHandler — bad ID
// ---------------------------------------------------------------------------

func TestRouteTrackHandler_BadID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/abc/track", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	routeTrackHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// companyCreateHandler — invalid JSON body
// ---------------------------------------------------------------------------

func TestCompanyCreateHandler_InvalidJSON(t *testing.T) {
	oldTenant := appTenant
	appTenant = nil
	defer func() { appTenant = oldTenant }()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/companies",
		strings.NewReader(`{invalid`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(ctxUserKey, models.AuthUser{ID: 1, CompanyCode: "default", Role: "SuperAdmin"})
	c.Set(ctxAdminKey, true)
	c.Set(ctxCompanyCodeKey, "default")

	companyCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}
