package controllers

// coverage_remaining_test.go (B4 coverage api-vehicle 2026-09-08):
// Menutup 5 fungsi 0%: userVehicleIDs, recordFailedLogin, healthzHandler,
// fuelConfigsUpdateHandler, fuelConfigsDeleteHandler.

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// 1. userVehicleIDs (auth.go:174) — 0%
// ---------------------------------------------------------------------------

func TestUserVehicleIDs(t *testing.T) {
	db, mock := mockDB(t)
	rows := sqlmock.NewRows([]string{"vehicle_id"}).AddRow(10).AddRow(20).AddRow(30)
	mock.ExpectQuery("SELECT vehicle_id FROM user_vehicles").WillReturnRows(rows)

	ids, err := userVehicleIDs(db, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 vehicles, got %d", len(ids))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestUserVehicleIDs_Error(t *testing.T) {
	db, mock := mockDB(t)
	mock.ExpectQuery("SELECT vehicle_id FROM user_vehicles").WillReturnError(sql.ErrConnDone)

	ids, err := userVehicleIDs(db, 7)
	if err == nil {
		t.Fatal("expected error")
	}
	if ids != nil {
		t.Fatalf("expected nil ids, got %v", ids)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// 2. recordFailedLogin (auth_login.go:193) — 0%
// ---------------------------------------------------------------------------

func TestRecordFailedLogin(t *testing.T) {
	db, mock := mockDB(t)
	mock.ExpectExec("UPDATE users SET failed_login_attempts").WillReturnResult(sqlmock.NewResult(0, 1))

	recordFailedLogin(db, 42, 5, 15*60*1000000000) // 15 min in ns
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordFailedLogin_NilGuard(t *testing.T) {
	// nil db & threshold <= 0 harus no-op (tidak panic)
	recordFailedLogin(nil, 42, 5, 15*60*1000000000)
	recordFailedLogin(nil, 42, 0, 15*60*1000000000)
}

// ---------------------------------------------------------------------------
// 3. healthzHandler (base.go:142) — 0%
// ---------------------------------------------------------------------------

func TestHealthzHandler(t *testing.T) {
	// Save & restore
	oldCfg := appCfg
	oldRedis := appRedis
	oldTenant := appTenant
	t.Cleanup(func() {
		appCfg = oldCfg
		appRedis = oldRedis
		appTenant = oldTenant
	})

	appCfg = newTestCfg()
	appRedis = nil // nil → redis check skipped → degraded path
	appTenant = &tenant.Manager{} // zero value → Health() returns nil

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/healthz", nil)

	healthzHandler(c)
	// nil redis → "redis:down" → degraded 503 (valid branch coverage)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (degraded), got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "degraded") {
		t.Fatalf("expected degraded body, got %q", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 4. fuelConfigsUpdateHandler (handlers_fuel.go:223) — 0%
// ---------------------------------------------------------------------------

func TestFuelConfigsUpdateHandler(t *testing.T) {
	dialect.Set(dialect.MySQL) // force LastInsertId path
	t.Cleanup(func() { dialect.Set(dialect.Postgres) })

	body := `{"drop_threshold":15.5,"refuel_threshold":20.0,"window_seconds":600,"enabled":false}`
	c, rec, m := companyCtx(t, true, "PATCH", "/fuel-configs/5", body)
	m.ExpectExec("UPDATE fuel_configs SET").WillReturnResult(sqlmock.NewResult(0, 1))

	fuelConfigsUpdateHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"id\":5") {
		t.Fatalf("expected id 5 in response, got %s", rec.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFuelConfigsUpdateHandler_NotFound(t *testing.T) {
	dialect.Set(dialect.MySQL)
	t.Cleanup(func() { dialect.Set(dialect.Postgres) })

	body := `{"drop_threshold":15.5}`
	c, rec, m := companyCtx(t, true, "PATCH", "/fuel-configs/99", body)
	m.ExpectExec("UPDATE fuel_configs SET").WillReturnResult(sqlmock.NewResult(0, 0))

	fuelConfigsUpdateHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFuelConfigsUpdateHandler_EmptyUpdate(t *testing.T) {
	body := `{}`
	c, rec, _ := companyCtx(t, true, "PATCH", "/fuel-configs/5", body)
	fuelConfigsUpdateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 5. fuelConfigsDeleteHandler (handlers_fuel.go:297) — 0%
// ---------------------------------------------------------------------------

func TestFuelConfigsDeleteHandler(t *testing.T) {
	dialect.Set(dialect.MySQL)
	t.Cleanup(func() { dialect.Set(dialect.Postgres) })

	c, rec, m := companyCtx(t, true, "DELETE", "/fuel-configs/5", "")
	m.ExpectExec("DELETE FROM fuel_configs").WillReturnResult(sqlmock.NewResult(0, 1))

	fuelConfigsDeleteHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"id\":5") {
		t.Fatalf("expected id 5 in response, got %s", rec.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFuelConfigsDeleteHandler_RBAC(t *testing.T) {
	// Non-admin → 403
	c, rec, _ := companyCtx(t, false, "DELETE", "/fuel-configs/5", "")
	fuelConfigsDeleteHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
