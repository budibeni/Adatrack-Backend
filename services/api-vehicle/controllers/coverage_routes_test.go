package controllers

// coverage_routes_test.go (B4 2026-09-08): routes CRUD + assignment handlers.

import (
	"encoding/json"
	"net/http"
	"testing"

	"ajb_gps/api-vehicle/models"
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
// GET /routes — routesListHandler
// ---------------------------------------------------------------------

func TestRoutesListAdmin(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes", "")

	m.ExpectQuery("(?i)FROM routes ORDER BY id").
		WillReturnRows(sqlmock.NewRows(avRouteCols).AddRow(avRouteRow(1, "R1", []byte(`[{"lat":1,"lon":2}]`), 60)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avAssignCols).AddRow(avAssignRow(10, 1, 5, 7, "in_progress")...))

	routesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("routes list = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRoutesListNonAdminFiltered(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, false, "GET", "/routes", "")

	m.ExpectQuery("(?i)FROM routes ORDER BY id").
		WillReturnRows(sqlmock.NewRows(avRouteCols).AddRow(avRouteRow(1, "R1", []byte(`[{"lat":1,"lon":2}]`), nil)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avAssignCols).AddRow(avAssignRow(10, 1, 1, 7, "in_progress")...))

	routesListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("routes list non-admin = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// POST /routes — routesCreateHandler
// ---------------------------------------------------------------------

func TestRoutesCreateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes",
		`{"name":"R1","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]}`)
	wps, _ := json.Marshal([]models.Waypoint{{Lat: -6.2, Lon: 106.8}, {Lat: -6.3, Lon: 106.9}})

	m.ExpectExec("(?i)INSERT INTO routes \\(name, waypoints, estimated_duration_sec, created_by, is_active\\)").
		WithArgs("R1", wps, nil, uint64(7)).WillReturnResult(sqlmock.NewResult(8, 1))

	routesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("route create = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRoutesCreateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/routes", `{"name":"R1","waypoints":[{"lat":1,"lon":2},{"lat":3,"lon":4}]}`)

	routesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("route create operator = %d, want 403", code)
	}
}

func TestRoutesCreateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/routes", `{"name":"R1","waypoints":[{"lat":1,"lon":2}]}`)

	routesCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("route create bad = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// GET /routes/:id — routesDetailHandler
// ---------------------------------------------------------------------

func TestRoutesDetailFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes/1", "")
	c.AddParam("id", "1")

	m.ExpectQuery("(?i)FROM routes WHERE id = \\?").
		WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(avRouteCols).AddRow(avRouteRow(1, "R1", []byte(`[]`), nil)...))
	m.ExpectQuery("(?i)FROM route_assignments WHERE route_id = \\? ORDER BY id").
		WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows(avAssignCols))

	routesDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("route detail = %d, want 200", code)
	}
}

func TestRoutesDetailNotFound2(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/routes/404", "")
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)FROM routes WHERE id = \\?").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows(avRouteCols))

	routesDetailHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("route detail 404 = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------
// PATCH /routes/:id — routesUpdateHandler
// ---------------------------------------------------------------------

func TestRoutesUpdateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1", `{"name":"R2"}`)
	c.AddParam("id", "1")

	m.ExpectExec("(?i)UPDATE routes SET name = \\? WHERE id = \\?").
		WithArgs("R2", uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))

	routesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("route update = %d, want 200", code)
	}
}

func TestRoutesUpdateEmpty(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1", `{}`)
	c.AddParam("id", "1")

	routesUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("route update empty = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// DELETE /routes/:id — routesDeleteHandler
// ---------------------------------------------------------------------

func TestRoutesDeleteSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1", "")
	c.AddParam("id", "1")

	m.ExpectExec("(?i)UPDATE routes SET is_active = FALSE WHERE id = \\?").
		WithArgs(uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))

	routesDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("route delete = %d, want 200", code)
	}
}

// ---------------------------------------------------------------------
// POST /routes/:id/assignments — routeAssignHandler
// ---------------------------------------------------------------------

func TestRouteAssignSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes/1/assignments", `{"vehicle_id":5,"driver_user_id":7}`)
	c.AddParam("id", "1")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM routes WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(1)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectExec("(?i)INSERT INTO route_assignments \\(route_id, vehicle_id, driver_user_id, status\\)").
		WithArgs(uint64(1), uint64(5), uint64(7)).WillReturnResult(sqlmock.NewResult(22, 1))

	routeAssignHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("route assign = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRouteAssignRouteNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/routes/404/assignments", `{"vehicle_id":5,"driver_user_id":7}`)
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM routes WHERE id=\\? AND is_active=TRUE").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	routeAssignHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("route assign 404 = %d, want 404", code)
	}
}

// ---------------------------------------------------------------------
// PATCH /routes/:id/assignments/:assignmentId — routeAssignmentStatusHandler
// ---------------------------------------------------------------------

func TestRouteAssignmentStatusInProgress(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/routes/1/assignments/10", `{"status":"in_progress"}`)
	// companyCtx extracts trailing numeric segment as :id (10); override with correct params.
	c.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "assignmentId", Value: "10"}}

	m.ExpectExec("(?i)UPDATE route_assignments SET status = \\?, started_at = COALESCE\\(started_at, NOW\\(\\)\\) WHERE id = \\? AND route_id = \\?").
		WithArgs("in_progress", uint64(10), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))

	routeAssignmentStatusHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("status update = %d, want 200", code)
	}
}

func TestRouteAssignmentStatusBadValue(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/routes/1/assignments/10", `{"status":"bogus"}`)
	c.AddParam("id", "1")
	c.AddParam("assignmentId", "10")

	routeAssignmentStatusHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("status bad = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// DELETE /routes/:id/assignments/:assignmentId — routeUnassignHandler
// ---------------------------------------------------------------------

func TestRouteUnassignSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/routes/1/assignments/10", "")
	// companyCtx extracts trailing numeric segment as :id (10); override with correct params.
	c.Params = gin.Params{{Key: "id", Value: "1"}, {Key: "assignmentId", Value: "10"}}

	m.ExpectExec("(?i)DELETE FROM route_assignments WHERE id = \\? AND route_id = \\?").
		WithArgs(uint64(10), uint64(1)).WillReturnResult(sqlmock.NewResult(0, 1))

	routeUnassignHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("unassign = %d, want 200", code)
	}
}

func TestRouteUnassignBadID(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "DELETE", "/routes/1/assignments/abc", "")
	c.AddParam("id", "1")
	c.AddParam("assignmentId", "abc")

	routeUnassignHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("unassign bad id = %d, want 400", code)
	}
}