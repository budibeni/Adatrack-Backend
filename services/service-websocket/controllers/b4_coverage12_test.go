package controllers

// b4_coverage12_test.go (B4 coverage 2026-09-05 — Stage I):
// routes handlers — detail/track/delete/update/create/list (sqlmock + admin ctx).

import (
	dbSql "database/sql"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

var routeScanCols = []string{
	"id", "name", "waypoints", "estimated_duration_sec", "created_by",
	"is_active", "created_at", "updated_at",
}

func routeRowVals12(id uint64, name string) []drvVal12 {
	return []drvVal12{
		uint64(id), name, []byte(`[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]`),
		int64(3600), uint64(9), true, time.Now(), time.Now(),
	}
}

// drvVal12 is a tiny alias to make routeRow values satisfy sqlmock.AddRow.
type drvVal12 = driver.Value

func assignEmpty(m sqlmock.Sqlmock) {
	m.ExpectQuery(`SELECT ra\.id, ra\.route_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "route_id", "vehicle_id", "driver_user_id", "status",
			"started_at", "completed_at", "deviation_meters", "imei",
		}))
}

func TestRouteDetailHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(1, "Route A")...))
	assignEmpty(m)

	routeDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Route A") {
		t.Errorf("missing route name: %s", rec.Body.String())
	}
}

func TestRouteDetailHandler_NotFound_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/99", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "99"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(99)).
		WillReturnError(dbSql.ErrNoRows)

	routeDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDetailHandler_BadID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/abc", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	routeDetailHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteTrackHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/1/track", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(1, "Route A")...))
	assignEmpty(m)

	routeTrackHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "route_id") {
		t.Errorf("missing route_id in track: %s", rec.Body.String())
	}
}

func TestRouteTrackHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes/9/track", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "9"}}

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(9)).
		WillReturnError(dbSql.ErrNoRows)

	routeTrackHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDeleteHandler_Success_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/3", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	m.ExpectExec(`UPDATE routes SET is_active = FALSE`).WithArgs(uint64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	routeDeleteHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDeleteHandler_NotFound_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/8", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "8"}}

	m.ExpectExec(`UPDATE routes SET is_active = FALSE`).WithArgs(uint64(8)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	routeDeleteHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRouteDeleteHandler_NonAdmin_V2(
	t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/routes/3", db, false, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	routeDeleteHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routesUpdateHandler — PATCH (name + waypoints + assignment)
// ---------------------------------------------------------------------------

func postRouteJSON(c *gin.Context, body string) {
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/routes/1", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
}

func TestRoutesUpdateHandler_Success_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/routes/1", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	postRouteJSON(c, `{"name":"Route X","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.4,"lon":106.9}],"estimated_duration_sec":1800,"vehicle_ids":[7]}`)

	// fetch existing route.
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(1, "Old")...))
	// append assignment.
	m.ExpectExec(`INSERT IGNORE INTO route_assignments`).WithArgs(uint64(1), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// update sets.
	m.ExpectExec(`UPDATE routes SET name = \?, waypoints = \?, estimated_duration_sec = \? WHERE id = \? AND is_active = TRUE`).
		WithArgs("Route X", sqlmock.AnyArg(), int(1800), uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// reload.
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(1, "Route X")...))
	assignEmpty(m)

	routesUpdateHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Route X") {
		t.Errorf("missing updated name: %s", rec.Body.String())
	}
}

func TestRoutesUpdateHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/routes/99", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "99"}}
	postRouteJSON(c, `{"name":"X"}`)

	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(99)).
		WillReturnError(dbSql.ErrNoRows)

	routesUpdateHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRoutesUpdateHandler_NonAdmin(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/routes/1", db, false, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	postRouteJSON(c, `{"name":"X"}`)

	routesUpdateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routesCreateHandler — POST (admin)
// ---------------------------------------------------------------------------

func TestRoutesCreateHandler_Success_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, true, nil)
	body := `{"name":"Route C","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}],"vehicle_ids":[7]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	m.ExpectBegin()
	m.ExpectExec(`INSERT INTO routes \(name, waypoints`).
		WithArgs("Route C", sqlmock.AnyArg(), nil, uint64(9)).
		WillReturnResult(sqlmock.NewResult(10, 1))
	m.ExpectExec(`INSERT INTO route_assignments`).WithArgs(int64(10), uint64(7), nil).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	m.ExpectQuery(`SELECT id, name, waypoints, estimated_duration_sec`).WithArgs(uint64(10)).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(10, "Route C")...))
	assignEmpty(m)

	routesCreateHandler(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRoutesCreateHandler_ValidationAndNonAdmin(t *testing.T) {
	// Non-admin → 403.
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db, false, nil)
	body := `{"name":"R","waypoints":[{"lat":-6.2,"lon":106.8},{"lat":-6.3,"lon":106.9}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	routesCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	// Admin dengan <2 waypoints → 400.
	db2, _ := mockDB(t)
	c2, rec2 := ginCtxWithDB(http.MethodPost, "/api/v1/routes", db2, true, nil)
	body2 := `{"name":"R","waypoints":[{"lat":-6.2,"lon":106.8}]}`
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/routes", strings.NewReader(body2))
	c2.Request.Header.Set("Content-Type", "application/json")
	routesCreateHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// routesListHandler — admin vs non-admin (no allowed)
// ---------------------------------------------------------------------------

func TestRoutesListHandler_Admin_V2(
	t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes", db, true, nil)

	m.ExpectQuery(`SELECT r\.id, r\.name, r\.waypoints`).
		WillReturnRows(sqlmock.NewRows(routeScanCols).AddRow(routeRowVals12(1, "Route A")...))
	assignEmpty(m)

	routesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRoutesListHandler_OperatorEmpty(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/routes", db, false, map[uint64]struct{}{})

	routesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
