package controllers

// coverage_more_handlers_test.go (B4 coverage api-vehicle 2026-09-10):
// menutup error/edge paths handler yang belum tercakup — routes update/assign,
// vehicle write/users/list/detail, speed configs, geofence, vehicle create, dan
// fuel history — memakai pola companyCtx + sqlmock dari b4_mock_test.go.

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"ajb_gps/api-vehicle/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// noCompanyDB mengosongkan pool company di context sehingga companyDB()/
// companyRead() mengembalikan error (jalur 500 handlers).
func noCompanyDB(c *gin.Context) {
	c.Set(ctxCompanyDBKey, nil)
	c.Set(ctxCompanyROKey, nil)
}

// ginParams membangun gin.Params dari pasangan key/value.
func ginParams(kv ...string) gin.Params {
	p := make(gin.Params, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		p = append(p, gin.Param{Key: kv[i], Value: kv[i+1]})
	}
	return p
}

// ---------------------------------------------------------------------------
// handlers_routes_update.go — routesUpdateHandler / routesDeleteHandler
// ---------------------------------------------------------------------------

func TestRoutesUpdateBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/abc", `{"name":"X"}`)
	c.AddParam("id", "abc")
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("routes update bad id = %d, want 400", code)
	}
}

func TestRoutesUpdateNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "PATCH", "/routes/1", `{"name":"X"}`)
	c.AddParam("id", "1")
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("routes update non-admin = %d, want 403", code)
	}
}

func TestRoutesUpdateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1", `{"name":`)
	c.AddParam("id", "1")
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("routes update bad body = %d, want 400", code)
	}
}

func TestRoutesUpdateDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1", `{"name":"X"}`)
	c.AddParam("id", "1")
	noCompanyDB(c)
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("routes update db err = %d, want 500", code)
	}
}

func TestRoutesUpdateMultiFieldSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/5",
		`{"name":"R2","waypoints":[{"lat":1,"lon":2},{"lat":3,"lon":4}],"estimated_duration_sec":600,"is_active":false}`)
	c.AddParam("id", "5")
	wps, _ := json.Marshal([]models.Waypoint{{Lat: 1, Lon: 2}, {Lat: 3, Lon: 4}})

	m.ExpectExec("(?i)UPDATE routes SET name = \\?, waypoints = \\?, estimated_duration_sec = \\?, is_active = \\? WHERE id = \\?").
		WithArgs("R2", wps, 600, false, uint64(5)).WillReturnResult(sqlmock.NewResult(0, 1))

	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("routes update multi = %d, want 200", code)
	}
}

func TestRoutesUpdateEmptyFields(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1", `{}`)
	c.AddParam("id", "1")
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("routes update empty = %d, want 400", code)
	}
}

func TestRoutesUpdateExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1", `{"name":"X"}`)
	c.AddParam("id", "1")
	m.ExpectExec("(?i)UPDATE routes SET").WillReturnError(errors.New("boom"))
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("routes update exec err = %d, want 500", code)
	}
}

func TestRoutesUpdateMissing(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1", `{"name":"X"}`)
	c.AddParam("id", "1")
	m.ExpectExec("(?i)UPDATE routes SET").WillReturnResult(sqlmock.NewResult(0, 0))
	routesUpdateHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("routes update 404 = %d, want 404", code)
	}
}

func TestRoutesDeleteNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")
	routesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("routes delete non-admin = %d, want 403", code)
	}
}

func TestRoutesDeleteDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")
	noCompanyDB(c)
	routesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("routes delete db err = %d, want 500", code)
	}
}

func TestRoutesDeleteExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")
	m.ExpectExec("(?i)UPDATE routes SET is_active = FALSE").WillReturnError(errors.New("boom"))
	routesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("routes delete exec err = %d, want 500", code)
	}
}

func TestRoutesDeleteMissing(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")
	m.ExpectExec("(?i)UPDATE routes SET is_active = FALSE").WillReturnResult(sqlmock.NewResult(0, 0))
	routesDeleteHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("routes delete 404 = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------------
// handlers_routes_assign.go — routeAssignHandler / routeAssignmentStatusHandler
// / routeUnassignHandler error paths
// ---------------------------------------------------------------------------

func TestRouteAssignBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/routes/abc/assignments", `{"vehicle_id":1,"driver_user_id":2}`)
	c.AddParam("id", "abc")
	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("assign bad id = %d, want 400", code)
	}
}

func TestRouteAssignNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/routes/1/assignments", `{"vehicle_id":1,"driver_user_id":2}`)
	c.AddParam("id", "1")
	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("assign non-admin = %d, want 403", code)
	}
}

func TestRouteAssignBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":0}`)
	c.AddParam("id", "1")
	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("assign bad body = %d, want 400", code)
	}
}

func TestRouteAssignVehicleForbidden(t *testing.T) {
	t.Helper()
	// Operator allowed hanya vehicle 1 & 2 — assign vehicle 9 → 403
	c, _, _ := companyCtx(t, false, "POST", "/routes/1/assignments", `{"vehicle_id":9,"driver_user_id":2}`)
	c.AddParam("id", "1")
	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("assign vehicle no-access = %d, want 403", code)
	}
}

func TestRouteAssignDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":1,"driver_user_id":2}`)
	c.AddParam("id", "1")
	noCompanyDB(c)
	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("assign db err = %d, want 500", code)
	}
}

func TestRouteAssignExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":5,"driver_user_id":7}`)
	c.AddParam("id", "1")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM routes WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT INTO route_assignments").WillReturnError(errors.New("boom"))

	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("assign exec err = %d, want 500", code)
	}
}

func TestRouteAssignVehicleMissing(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":99,"driver_user_id":7}`)
	c.AddParam("id", "1")
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM routes WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(99)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	routeAssignHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("assign vehicle 404 = %d, want 404", code)
	}
}
func TestRouteAssignmentStatusBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/abc/assignments/1", `{"status":"in_progress"}`)
	c.Params = ginParams("id", "abc", "assignmentId", "1")
	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("status bad id = %d, want 400", code)
	}
}

func TestRouteAssignmentStatusBadAssignmentID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1/assignments/abc", `{"status":"in_progress"}`)
	c.Params = ginParams("id", "1", "assignmentId", "abc")
	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("status bad assignment id = %d, want 400", code)
	}
}

func TestRouteAssignmentStatusNonAdmin(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "PATCH", "/routes/1/assignments/1", `{"status":"in_progress"}`)
	c.Params = ginParams("id", "1", "assignmentId", "1")
	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("status non-admin = %d, want 403", code)
	}
}

func TestRouteAssignmentStatusDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1/assignments/1", `{"status":"in_progress"}`)
	c.Params = ginParams("id", "1", "assignmentId", "1")
	noCompanyDB(c)
	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("status db err = %d, want 500", code)
	}
}

func TestRouteAssignmentStatusCompleted(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1/assignments/10", `{"status":"completed"}`)
	c.Params = ginParams("id", "1", "assignmentId", "10")
	m.ExpectExec("(?i)UPDATE route_assignments SET status = \\?, completed_at = COALESCE\\(completed_at, NOW\\(\\)\\) WHERE id = \\? AND route_id = \\?").
		WithArgs("completed", uint64(10), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))

	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("status completed = %d, want 200", code)
	}
}

func TestRouteAssignmentStatusExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1/assignments/10", `{"status":"delayed"}`)
	c.Params = ginParams("id", "1", "assignmentId", "10")
	m.ExpectExec("(?i)UPDATE route_assignments SET status").WillReturnError(errors.New("boom"))

	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("status exec err = %d, want 500", code)
	}
}

func TestRouteAssignmentStatusNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1/assignments/10", `{"status":"delayed"}`)
	c.Params = ginParams("id", "1", "assignmentId", "10")
	m.ExpectExec("(?i)UPDATE route_assignments SET status").WillReturnResult(sqlmock.NewResult(0, 0))

	routeAssignmentStatusHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("status 404 = %d, want 404", code)
	}
}

func TestRouteUnassignBadRouteID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/routes/abc/assignments/1", "")
	c.Params = ginParams("id", "abc", "assignmentId", "1")
	routeUnassignHandler(c)
	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("unassign bad id = %d, want 400", code)
	}
}

func TestRouteUnassignDBError(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/routes/1/assignments/10", "")
	c.Params = ginParams("id", "1", "assignmentId", "10")
	noCompanyDB(c)
	routeUnassignHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("unassign db err = %d, want 500", code)
	}
}

func TestRouteUnassignExecError(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1/assignments/10", "")
	c.Params = ginParams("id", "1", "assignmentId", "10")
	m.ExpectExec("(?i)DELETE FROM route_assignments WHERE id = \\? AND route_id = \\?").
		WithArgs(uint64(10), uint64(1)).WillReturnError(errors.New("boom"))

	routeUnassignHandler(c)
	if code := c.Writer.Status(); code != http.StatusInternalServerError {
		t.Fatalf("unassign exec err = %d, want 500", code)
	}
}

func TestRouteUnassignNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1/assignments/10", "")
	c.Params = ginParams("id", "1", "assignmentId", "10")
	m.ExpectExec("(?i)DELETE FROM route_assignments WHERE id = \\? AND route_id = \\?").
		WithArgs(uint64(10), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 0))

	routeUnassignHandler(c)
	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("unassign 404 = %d, want 404", code)
	}
}