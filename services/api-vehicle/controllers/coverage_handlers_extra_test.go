package controllers

// coverage_handlers_extra_test.go (B4 coverage api-vehicle 2026-09-09):
// menutup error/edge paths handler: routes detail/list/delete, fuel & speed
// list errors, vehicle assign/update/delete error paths, loadMasterUserByEmail
// nullable fields.

import (
	"errors"
	"net/http"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// ---------------------------------------------------------------------
// routes detail — scan error / assignment error
// ---------------------------------------------------------------------

func TestRoutesDetailScanError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes/1", "")
	c.AddParam("id", "1")

	// Waypoints JSON garbage → scanRouteItem json.Unmarshal gagal (ignored),
	// tapi kolom lain harus valid.
	m.ExpectQuery("(?i)FROM routes WHERE id = \\?$").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avRouteCols).
			AddRow(avRouteRow(1, "R1", []byte(`{{{bad`), nil)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id$").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avAssignCols))

	routesDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("route detail scan err = %d, want 200 (unmarshal fail is ignored)", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRoutesDetailAssignmentError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes/1", "")
	c.AddParam("id", "1")

	m.ExpectQuery("(?i)FROM routes WHERE id = \\?$").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avRouteCols).
			AddRow(avRouteRow(1, "R1", []byte(`[{"lat":1,"lon":2}]`), 60)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id$").
		WithArgs(uint64(1)).WillReturnError(errors.New("boom"))

	routesDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("route detail assign err = %d, want 500", code)
	}
}

func TestRoutesDetailQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes/1", "")
	c.AddParam("id", "1")

	m.ExpectQuery("(?i)FROM routes WHERE id = \\?$").
		WithArgs(uint64(1)).WillReturnError(errors.New("boom"))

	routesDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("route detail query err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// routes list — assignments load error → continue (list tetap 200)
// ---------------------------------------------------------------------

func TestRoutesListAssignmentsErrorContinues(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes", "")

	m.ExpectQuery("(?i)FROM routes ORDER BY id").
		WillReturnRows(sqlmock.NewRows(avRouteCols).
			AddRow(avRouteRow(1, "R1", []byte(`[{"lat":1,"lon":2}]`), 60)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id$").
		WithArgs(uint64(1)).WillReturnError(errors.New("boom"))

	routesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("routes list assign err = %d, want 200 (degraded but not fatal)", code)
	}
}

// ---------------------------------------------------------------------
// routes delete — not found
// ---------------------------------------------------------------------

func TestRoutesDeleteNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/404", "")
	c.AddParam("id", "404")

	m.ExpectExec("(?i)UPDATE routes SET is_active = FALSE WHERE id = \\?$").
		WithArgs(uint64(404)).WillReturnResult(sqlmock.NewResult(0, 0))

	routesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("routes delete 404 = %d, want 404", code)
	}
}

func TestRoutesDeleteOperator(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")

	routesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("routes delete operator = %d, want 403", code)
	}
}

// ---------------------------------------------------------------------
// fuel / speed configs list — query error
// ---------------------------------------------------------------------

func TestFuelConfigsListQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/fuel-configs", "")

	m.ExpectQuery("(?i)FROM fuel_configs ORDER BY").WillReturnError(errors.New("boom"))

	fuelConfigsListHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("fuel configs list err = %d, want 500", code)
	}
}

func TestSpeedConfigsListQueryError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/speed-configs", "")

	m.ExpectQuery("(?i)FROM speed_configs ORDER BY").WillReturnError(errors.New("boom"))

	speedConfigsListHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("speed configs list err = %d, want 500", code)
	}
}

// ---------------------------------------------------------------------
// vehicle assign/unassign — error paths
// ---------------------------------------------------------------------

func TestVehicleAssignUserVehicleNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles/404/users", `{"user_id":5}`)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("assign vehicle 404 = %d, want 404", code)
	}
}

func TestVehicleAssignUserInsertError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/vehicles/5/users", `{"user_id":5}`)
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM user_company_access WHERE user_id=\\? AND is_active=TRUE").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT IGNORE INTO user_vehicles \\(user_id, vehicle_id\\)").
		WithArgs(uint64(5), uint64(5)).WillReturnError(errors.New("boom"))

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("assign insert err = %d, want 500", code)
	}
}

func TestVehicleAssignUserBadJSON(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/vehicles/5/users", `{"user_id":0}`)
	c.AddParam("id", "5")

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("assign bad json = %d, want 400", code)
	}
}

func TestVehicleAssignUserNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/vehicles/5/users", `{"user_id":5}`)
	c.AddParam("id", "5")

	vehicleAssignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("assign non-admin = %d, want 403", code)
	}
}

func TestVehicleUnassignUserNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/vehicles/5/users/7", "")
	c.AddParam("id", "5")
	c.AddParam("userId", "7")

	vehicleUnassignUserHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("unassign non-admin = %d, want 403", code)
	}
}
