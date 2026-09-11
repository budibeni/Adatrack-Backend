package controllers

// coverage_speed_fuel_test.go (B4 2026-09-08): speed_configs CRUD + fuel
// history & fuel_configs CRUD handlers, based on sqlmock.

import (
	"net/http"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/dialect"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	gin.SetMode(gin.TestMode)
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

// ---------------------------------------------------------------------
// GET /speed-configs — speedConfigsListHandler
// ---------------------------------------------------------------------

func TestSpeedConfigsList(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/speed-configs", "")

	m.ExpectQuery("(?i)FROM speed_configs ORDER BY").
		WillReturnRows(sqlmock.NewRows([]string{"id", "vehicle_id", "speed_limit_kmh", "grace_margin_kmh", "is_active", "created_at"}).
			AddRow(uint64(1), nil, 80.0, 10.0, true, time.Now()))

	speedConfigsListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("speed list = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// POST /speed-configs — speedConfigsCreateHandler
// ---------------------------------------------------------------------

func TestSpeedConfigsCreateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/speed-configs", `{"speed_limit_kmh":80,"grace_margin_kmh":10}`)

	speedConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("speed create operator = %d, want 403", code)
	}
}

func TestSpeedConfigsCreateBadBody(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/speed-configs", `{"speed_limit_kmh":0}`)

	speedConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed create bad = %d, want 400", code)
	}
}

func TestSpeedConfigsCreateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/speed-configs", `{"speed_limit_kmh":80,"grace_margin_kmh":10}`)

	m.ExpectExec("(?i)INSERT INTO speed_configs \\(vehicle_id, speed_limit_kmh, grace_margin_kmh, is_active\\)").
		WithArgs(nil, 80.0, 10.0).WillReturnResult(sqlmock.NewResult(9, 1))

	speedConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("speed create = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

// ---------------------------------------------------------------------
// PATCH/DELETE /speed-configs/:id
// ---------------------------------------------------------------------

func TestSpeedConfigsUpdateSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "PATCH", "/speed-configs/3", `{"speed_limit_kmh":90}`)
	c.AddParam("id", "3")

	m.ExpectExec("(?i)UPDATE speed_configs SET speed_limit_kmh = \\? WHERE id = \\?").
		WithArgs(90.0, uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))

	speedConfigsUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("speed update = %d, want 200", code)
	}
}

func TestSpeedConfigsUpdateEmpty(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/speed-configs/3", `{}`)
	c.AddParam("id", "3")

	speedConfigsUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed update empty = %d, want 400", code)
	}
}

func TestSpeedConfigsUpdateZeroLimit(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "PATCH", "/speed-configs/3", `{"speed_limit_kmh":-1}`)
	c.AddParam("id", "3")

	speedConfigsUpdateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("speed update zero = %d, want 400", code)
	}
}

func TestSpeedConfigsDeleteSuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "DELETE", "/speed-configs/3", "")
	c.AddParam("id", "3")

	m.ExpectExec("(?i)DELETE FROM speed_configs WHERE id = \\?").
		WithArgs(uint64(3)).WillReturnResult(sqlmock.NewResult(0, 1))

	speedConfigsDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("speed delete = %d, want 200", code)
	}
}

func TestSpeedConfigsDeleteOperatorForbidden2(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "DELETE", "/speed-configs/3", "")
	c.AddParam("id", "3")

	speedConfigsDeleteHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("speed delete operator = %d, want 403", code)
	}
}

// ---------------------------------------------------------------------
// GET /vehicles/:id/fuel/history — vehicleFuelHistoryHandler
// ---------------------------------------------------------------------

func TestFuelHistorySuccess(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true,
		"GET", "/vehicles/5/fuel/history?from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z", "")
	c.AddParam("id", "5")
	fromT, ferr := time.Parse(time.RFC3339, "2026-08-01T00:00:00Z")
	toT, terr := time.Parse(time.RFC3339, "2026-08-02T00:00:00Z")
	if ferr != nil || terr != nil {
		t.Fatal("parse test times failed")
	}

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))
	m.ExpectQuery("(?i)SELECT fuel_level, fuel_volume, fuel_temp_c, timestamp").
		WithArgs(uint64(5), fromT, toT, 5000).
		WillReturnRows(sqlmock.NewRows([]string{"fuel_level", "fuel_volume", "fuel_temp_c", "timestamp"}).
			AddRow(40.0, 20.0, 25.0, time.Now()))

	vehicleFuelHistoryHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("fuel history = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuelHistoryVehicleNotFound(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/404/fuel/history", "")
	c.AddParam("id", "404")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(404)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(0)))

	vehicleFuelHistoryHandler(c)

	if code := c.Writer.Status(); code != http.StatusNotFound {
		t.Fatalf("fuel history 404 = %d, want 404", code)
	}
}

func TestFuelHistoryBadFrom(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles/5/fuel/history?from=zzz", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))

	vehicleFuelHistoryHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel history bad from = %d, want 400", code)
	}
}

func TestFuelHistoryToBeforeFrom(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true,
		"GET", "/vehicles/5/fuel/history?from=2026-01-02T00:00:00Z&to=2026-01-01T00:00:00Z", "")
	c.AddParam("id", "5")

	m.ExpectQuery("(?i)SELECT COUNT\\(\\*\\) FROM vehicles WHERE id=\\? AND deleted_at IS NULL").
		WithArgs(uint64(5)).WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(int64(1)))

	vehicleFuelHistoryHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel history to<from = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// GET/POST /fuel-configs — fuelConfigsListHandler / fuelConfigsCreateHandler
// ---------------------------------------------------------------------

func TestFuelConfigsList(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/fuel-configs", "")

	m.ExpectQuery("(?i)SELECT " + fuelCols + " FROM fuel_configs ORDER BY \\(vehicle_id IS NULL\\) DESC, id").
		WillReturnRows(sqlmock.NewRows(avFuelConfigCols).AddRow(avFuelConfigRow(1, nil, 10.0, 5.0, 300, true)...))

	fuelConfigsListHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("fuel configs list = %d, want 200", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuelConfigsCreateBadThresh(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, true, "POST", "/fuel-configs", `{"drop_threshold":0}`)

	fuelConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("fuel create bad = %d, want 400", code)
	}
}

func TestFuelConfigsCreateGlobal(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "POST", "/fuel-configs", `{"drop_threshold":10,"refuel_threshold":5,"window_seconds":60}`)

	old := dialect.Current()
	dialect.Set(dialect.MySQL)
	defer dialect.Set(old)

	m.ExpectExec("(?i)INSERT INTO fuel_configs \\(vehicle_id, drop_threshold, refuel_threshold, window_seconds, enabled\\)").
		WithArgs(nil, 10.0, 5.0, 60).WillReturnResult(sqlmock.NewResult(7, 1))

	fuelConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusCreated {
		t.Fatalf("fuel create = %d, want 201", code)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestFuelConfigsCreateOperatorForbidden(t *testing.T) {
	t.Helper()
	c, _, _ := companyCtx(t, false, "POST", "/fuel-configs", `{"drop_threshold":10,"refuel_threshold":5}`)

	fuelConfigsCreateHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("fuel create operator = %d, want 403", code)
	}
}