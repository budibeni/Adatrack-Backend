package controllers

// coverage_more_handlers2_test.go (B4 coverage api-vehicle 2026-09-10):
// error/edge paths vehicle write/users/list/detail, speed configs, geofence,
// vehicle create, dan fuel history — pola companyCtx + sqlmock.

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"ajb_gps/internal/dialect"

	"github.com/DATA-DOG/go-sqlmock"
)

// dialectSetForTest forces MySQL placeholder/dialect path (Exec + LastInsertId)
// lalu mengembalikan fungsi restore.
func dialectSetForTest(t *testing.T) func() {
	t.Helper()
	old := dialect.Current()
	dialect.Set(dialect.MySQL)
	return func() { dialect.Set(old) }
}

// ---------------------------------------------------------------------------
// handlers_vehicle_write.go — vehiclesUpdateHandler / vehiclesDeleteHandler
// ---------------------------------------------------------------------------

func TestVehicleUpdateNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "PATCH", "/vehicles/5", `{"plate_number":"B1"}`)
	c.AddParam("id", "5")
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("update non-admin = %d, want 403", code)
	}
}

func TestVehicleUpdateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/vehicles/5", `{"plate_number":`)
	c.AddParam("id", "5")
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("update bad body = %d, want 400", code)
	}
}

func TestVehicleUpdateBadStatus(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/vehicles/5", `{"status":"bogus"}`)
	c.AddParam("id", "5")
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("update bad status = %d, want 400", code)
	}
}

func TestVehicleUpdateDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/vehicles/5", `{"plate_number":"B1"}`)
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("update db err = %d, want 500", code)
	}
}

func TestVehicleUpdateExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/vehicles/5", `{"plate_number":"B1"}`)
	c.AddParam("id", "5")
	m.ExpectExec("(?i)UPDATE vehicles SET").WillReturnError(errors.New("boom"))
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("update exec err = %d, want 500", code)
	}
}

func TestVehicleUpdateMultiField(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/vehicles/5",
		`{"plate_number":"B2","make":"Toyota","model":"Avanza","status":"maintenance","driver_user_id":9}`)
	c.AddParam("id", "5")
	m.ExpectExec("(?i)UPDATE vehicles SET plate_number = \\?, make = \\?, model = \\?, status = \\?, driver_user_id = \\? WHERE id = \\? AND deleted_at IS NULL").
		WithArgs("B2", "Toyota", "Avanza", "maintenance", uint64(9), uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	vehiclesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("update multi = %d, want 200", code)
	}
}

func TestVehicleDeleteNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/vehicles/5", "")
	c.AddParam("id", "5")
	vehiclesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("delete non-admin = %d, want 403", code)
	}
}

func TestVehicleDeleteDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/vehicles/5", "")
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehiclesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("delete db err = %d, want 500", code)
	}
}

func TestVehicleDeleteNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/5", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT imei FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnError(sql.ErrNoRows)
	vehiclesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("delete 404 = %d, want 404", code)
	}
}

func TestVehicleDeleteExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/5", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT imei FROM vehicles WHERE id = \\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"imei"}).AddRow("999001"))
	m.ExpectExec("(?i)UPDATE vehicles SET deleted_at = NOW").WillReturnError(errors.New("boom"))
	vehiclesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("delete exec err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------------
// handlers_vehicle_users.go — vehicleUsersListHandler / vehicleAssignUserHandler
// / vehicleUnassignUserHandler error paths
// ---------------------------------------------------------------------------

func TestVehicleUsersListDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/5/users", "")
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehicleUsersListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("users list db err = %d, want 500", code)
	}
}

func TestVehicleUsersListBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/abc/users", "")
	c.AddParam("id", "abc")
	vehicleUsersListHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("users list bad id = %d, want 400", code)
	}
}

func TestVehicleAssignUserDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/vehicles/5/users", `{"user_id":9}`)
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehicleAssignUserHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("assign db err = %d, want 500", code)
	}
}

func TestVehicleUnassignBadUserID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/vehicles/5/users/abc", "")
	c.AddParam("id", "5")
	c.AddParam("userId", "abc")
	vehicleUnassignUserHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("unassign bad user = %d, want 400", code)
	}
}

func TestVehicleUnassignExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/vehicles/5/users/9", "")
	c.AddParam("id", "5")
	c.AddParam("userId", "9")
	m.ExpectExec("(?i)DELETE FROM user_vehicles WHERE vehicle_id = \\? AND user_id = \\?").
		WithArgs(uint64(5), uint64(9)).WillReturnError(errors.New("boom"))
	vehicleUnassignUserHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("unassign exec err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------------
// handlers_vehicle.go — vehiclesListHandler / vehicleDetailHandler
// ---------------------------------------------------------------------------

func TestVehicleListDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles", "")
	noCompanyDB(c)
	vehiclesListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("list db err = %d, want 500", code)
	}
}

func TestVehicleListScanError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")
	// created_at berupa string "invalid" → Scan ke time.Time gagal
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT id, imei, plate_number, make, model, fuel_type").WillReturnRows(
		sqlmock.NewRows(vehicleRowCols).AddRow(
			uint64(1), "999001", "B1", nil, nil, nil, "CAR", nil, "GT06", "active", "not-a-time"))
	vehiclesListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("list scan err = %d, want 500", code)
	}
}

func TestVehicleListNonAdminEmptyAllowed(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "GET", "/vehicles", "")
	c.Set(ctxAllowedKey, map[uint64]struct{}{})
	vehiclesListHandler(c)
	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("list empty allowed = %d, want 200", code)
	}
}

func TestVehicleDetailDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/5", "")
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehicleDetailHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("detail db err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------------
// handlers_speed.go — error paths
// ---------------------------------------------------------------------------

func TestSpeedConfigsListDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/speed-configs", "")
	noCompanyDB(c)
	speedConfigsListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed list db err = %d, want 500", code)
	}
}

func TestSpeedConfigsListScanError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/speed-configs", "")
	// handler mem-scan 6 dest; rows dg 5 kolom → Scan error → 500
	m.ExpectQuery("(?i)FROM speed_configs ORDER BY").WillReturnRows(
		sqlmock.NewRows([]string{"id", "vehicle_id", "speed_limit_kmh", "grace_margin_kmh", "is_active"}).
			AddRow(uint64(1), nil, 80.0, 10.0, true))
	speedConfigsListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed list scan err = %d, want 500", code)
	}
}

func TestSpeedConfigsCreateDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/speed-configs", `{"speed_limit_kmh":80}`)
	noCompanyDB(c)
	speedConfigsCreateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed create db err = %d, want 500", code)
	}
}

func TestSpeedConfigsCreateExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/speed-configs", `{"speed_limit_kmh":80,"vehicle_id":5}`)
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT INTO speed_configs").WillReturnError(errors.New("boom"))
	speedConfigsCreateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed create exec err = %d, want 500", code)
	}
}

func TestSpeedConfigsUpdateBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/speed-configs/abc", `{"speed_limit_kmh":90}`)
	c.AddParam("id", "abc")
	speedConfigsUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed update bad id = %d, want 400", code)
	}
}

func TestSpeedConfigsUpdateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "PATCH", "/speed-configs/1", `{"speed_limit_kmh":90}`)
	c.AddParam("id", "1")
	speedConfigsUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("speed update operator = %d, want 403", code)
	}
}

func TestSpeedConfigsUpdateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/speed-configs/1", `{"speed_limit_kmh":`)
	c.AddParam("id", "1")
	speedConfigsUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed update bad body = %d, want 400", code)
	}
}

func TestSpeedConfigsUpdateDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/speed-configs/1", `{"speed_limit_kmh":90}`)
	c.AddParam("id", "1")
	noCompanyDB(c)
	speedConfigsUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed update db err = %d, want 500", code)
	}
}

func TestSpeedConfigsUpdateExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/speed-configs/1", `{"speed_limit_kmh":90}`)
	c.AddParam("id", "1")
	m.ExpectExec("(?i)UPDATE speed_configs SET").WillReturnError(errors.New("boom"))
	speedConfigsUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed update exec err = %d, want 500", code)
	}
}

func TestSpeedConfigsDeleteBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/speed-configs/abc", "")
	c.AddParam("id", "abc")
	speedConfigsDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed delete bad id = %d, want 400", code)
	}
}

func TestSpeedConfigsDeleteDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/speed-configs/1", "")
	c.AddParam("id", "1")
	noCompanyDB(c)
	speedConfigsDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed delete db err = %d, want 500", code)
	}
}

func TestSpeedConfigsDeleteExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/speed-configs/1", "")
	c.AddParam("id", "1")
	m.ExpectExec("(?i)DELETE FROM speed_configs WHERE id = \\?").WillReturnError(errors.New("boom"))
	speedConfigsDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed delete exec err = %d, want 500", code)
	}
}

var _ = time.Now // keep time import used
// ---------------------------------------------------------------------------
// handlers_geofence.go — error paths
// ---------------------------------------------------------------------------

func TestGeofenceAddBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/geofences/5/vehicles", `{"vehicle_id":0}`)
	c.AddParam("id", "5")
	geofenceVehiclesAddHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("geofence add bad body = %d, want 400", code)
	}
}

func TestGeofenceAddVehicleAccessDenied(t *testing.T) {
	t.Helper()
	// Operator boleh vehicle 1 & 2; vehicle 9 → 403 via requireVehicleAccess
	c, _, _ := companyCtx(t, false, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")
	geofenceVehiclesAddHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("geofence add denied = %d, want 403", code)
	}
}

func TestGeofenceAddDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")
	noCompanyDB(c)
	geofenceVehiclesAddHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence add db err = %d, want 500", code)
	}
}

func TestGeofenceAddVehicleNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM geofences WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))
	geofenceVehiclesAddHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("geofence add vehicle 404 = %d, want 404", code)
	}
}

func TestGeofenceAddExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/geofences/5/vehicles", `{"vehicle_id":9}`)
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM geofences WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(9)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT IGNORE INTO geofence_vehicles").WillReturnError(errors.New("boom"))
	geofenceVehiclesAddHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence add exec err = %d, want 500", code)
	}
}

func TestGeofenceRemoveOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/geofences/5/vehicles/9", "")
	c.Params = ginParams("id", "5", "vehicleId", "9")
	geofenceVehiclesRemoveHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("geofence remove operator = %d, want 403", code)
	}
}

func TestGeofenceRemoveBadGeofenceID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/geofences/abc/vehicles/9", "")
	c.Params = ginParams("id", "abc", "vehicleId", "9")
	geofenceVehiclesRemoveHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("geofence remove bad id = %d, want 400", code)
	}
}

func TestGeofenceRemoveDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/geofences/5/vehicles/9", "")
	c.Params = ginParams("id", "5", "vehicleId", "9")
	noCompanyDB(c)
	geofenceVehiclesRemoveHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence remove db err = %d, want 500", code)
	}
}

func TestGeofenceRemoveExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/geofences/5/vehicles/9", "")
	c.Params = ginParams("id", "5", "vehicleId", "9")
	m.ExpectExec("(?i)DELETE FROM geofence_vehicles WHERE geofence_id = \\? AND vehicle_id = \\?").
		WithArgs(uint64(5), uint64(9)).WillReturnError(errors.New("boom"))
	geofenceVehiclesRemoveHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence remove exec err = %d, want 500", code)
	}
}

func TestGeofenceListDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/geofences/5/vehicles", "")
	c.AddParam("id", "5")
	noCompanyDB(c)
	geofenceVehiclesListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence list db err = %d, want 500", code)
	}
}

func TestGeofenceListQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/geofences/5/vehicles", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT gv\\.vehicle_id FROM geofence_vehicles").WillReturnError(errors.New("boom"))
	geofenceVehiclesListHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("geofence list query err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------------
// handlers_vehicle_create.go — create error path
// ---------------------------------------------------------------------------

func TestVehicleCreateExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles",
		`{"imei":"999000","plate_number":"B999XYZ"}`)
	m.ExpectExec("(?i)INSERT INTO vehicles").WillReturnError(errors.New("boom"))
	vehiclesCreateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("create exec err = %d, want 500", code)
	}
}
// ---------------------------------------------------------------------------
// handlers_fuel.go — fuel history + fuel configs error paths
// ---------------------------------------------------------------------------

func TestFuelHistoryBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/abc/fuel/history", "")
	c.AddParam("id", "abc")
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel history bad id = %d, want 400", code)
	}
}

func TestFuelHistoryDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "GET", "/vehicles/5/fuel/history", "")
	c.AddParam("id", "5")
	noCompanyDB(c)
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("fuel history db err = %d, want 500", code)
	}
}

func TestFuelHistoryCountError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/fuel/history", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnError(errors.New("boom"))
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("fuel history count err = %d, want 500", code)
	}
}

func TestFuelHistoryBadTo(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/fuel/history?to=bad", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel history bad to = %d, want 400", code)
	}
}

func TestFuelHistoryBadLimit(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/fuel/history?limit=99999", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel history bad limit = %d, want 400", code)
	}
}

func TestFuelHistoryScanError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/fuel/history", "")
	c.AddParam("id", "5")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT fuel_level, fuel_volume, fuel_temp_c, timestamp").
		WithArgs(uint64(5), sqlmock.AnyArg(), sqlmock.AnyArg(), 5000).WillReturnRows(
		sqlmock.NewRows([]string{"fuel_level", "fuel_volume", "fuel_temp_c", "timestamp"}).
			AddRow(40.0, 20.0, 25.0, "not-a-time"))
	vehicleFuelHistoryHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("fuel history scan err = %d, want 500", code)
	}
}

var _ = dialectSetForTest // reference helper below