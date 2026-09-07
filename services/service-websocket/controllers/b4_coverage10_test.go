package controllers

// b4_coverage10_test.go (B4 coverage 2026-09-05 — Stage G):
// registry.fetch success/nulls via companyReadByCodeFn override (sqlmock),
// userCreateHandler FULL success path (MySQL dialect, masterDBFn +
// companyDBByCodeFn overrides), vehiclesListHandler admin, vehicleDetail
// non-admin denial.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajb_gps/internal"
	"ajb_gps/internal/dialect"
	"ajb_gps/internal/tenant"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// registry.fetch — READ path (replica-preferred) success + nulls + error
// ---------------------------------------------------------------------------

func TestRegistryFetch_Success(t *testing.T) {
	oldRead := companyReadByCodeFn
	defer func() { companyReadByCodeFn = oldRead }()

	db, m := mockDB(t)
	companyReadByCodeFn = func(string) (*sql.DB, error) { return db, nil }

	m.ExpectQuery(`SELECT id, device_model, plate_number FROM vehicles WHERE imei = \? AND deleted_at IS NULL LIMIT 1`).
		WithArgs("111").
		WillReturnRows(sqlmock.NewRows([]string{"id", "device_model", "plate_number"}).
			AddRow(uint64(42), "GT06", "B 1234 XYZ"))

	info, err := (&vehicleRegistry{}).fetch("DEV001", "111")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if info.ID != 42 || info.Model != "GT06" || info.Plate != "B 1234 XYZ" {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestRegistryFetch_NullModelPlate(t *testing.T) {
	oldRead := companyReadByCodeFn
	defer func() { companyReadByCodeFn = oldRead }()

	db, m := mockDB(t)
	companyReadByCodeFn = func(string) (*sql.DB, error) { return db, nil }

	m.ExpectQuery(`SELECT id, device_model, plate_number`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "device_model", "plate_number"}).
			AddRow(uint64(7), nil, nil))

	info, err := (&vehicleRegistry{}).fetch("DEV001", "222")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if info.ID != 7 || info.Model != "" || info.Plate != "" {
		t.Errorf("expected nulls → empty strings, got %+v", info)
	}
}

func TestRegistryFetch_DBError(t *testing.T) {
	oldRead := companyReadByCodeFn
	defer func() { companyReadByCodeFn = oldRead }()

	db, m := mockDB(t)
	companyReadByCodeFn = func(string) (*sql.DB, error) { return db, nil }

	m.ExpectQuery(`SELECT id, device_model, plate_number`).
		WillReturnError(sql.ErrNoRows)

	if _, err := (&vehicleRegistry{}).fetch("DEV001", "333"); err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// userCreateHandler — FULL success path (platform admin + MySQL dialect)
// ---------------------------------------------------------------------------

func TestUserCreateHandler_Success(t *testing.T) {
	oldDialect := dialect.Current()
	dialect.Set(dialect.MySQL)
	defer dialect.Set(oldDialect)

	// appTenant di-set non-nil agar gate infra lolos; masterDBFn & companyDBByCodeFn di-override.
	oldTenant := appTenant
	appTenant = &tenant.Manager{}
	defer func() { appTenant = oldTenant }()

	oldMaster := masterDBFn
	oldCompany := companyDBByCodeFn
	defer func() { masterDBFn = oldMaster; companyDBByCodeFn = oldCompany }()

	master, m := mockDB(t)
	company, cm := mockDB(t)
	masterDBFn = func() *sql.DB { return master }
	companyDBByCodeFn = func(string) (*sql.DB, error) { return company, nil }

	// Master DB: company aktif, email belum ada, insert user → id 100.
	m.ExpectQuery(`SELECT id FROM companies WHERE code = \?`).WithArgs("DEV001").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(5)))
	m.ExpectQuery(`SELECT id FROM users WHERE email = \? AND deleted_at IS NULL`).WithArgs("new@dev001.io").
		WillReturnError(sql.ErrNoRows)
	m.ExpectExec(`INSERT INTO users`).
		WillReturnResult(sqlmock.NewResult(100, 1))

	// Company DB: tx upsert access + ownership check + assign vehicles + commit.
	cm.ExpectBegin()
	cm.ExpectExec(`INSERT INTO user_company_access`).WithArgs(uint64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	cm.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles WHERE id IN \(\?,\?\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(2)))
	cm.ExpectExec(`INSERT IGNORE INTO user_vehicles`).WithArgs(uint64(100), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	cm.ExpectExec(`INSERT IGNORE INTO user_vehicles`).WithArgs(uint64(100), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	cm.ExpectCommit()

	c, rec := platformUserCtx()
	body := `{"company_code":"DEV001","email":"new@dev001.io","password":"longenough1","full_name":"New User","role":"operator","vehicle_ids":[1,2]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	userCreateHandler(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "new@dev001.io") {
		t.Errorf("response missing email: %s", rec.Body.String())
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Errorf("master unmet: %v", err)
	}
	if err := cm.ExpectationsWereMet(); err != nil {
		t.Errorf("company unmet: %v", err)
	}
}

// ---------------------------------------------------------------------------
// vehiclesListHandler — admin path (all vehicles)
// ---------------------------------------------------------------------------

func TestVehiclesListHandler_Admin(t *testing.T) {
	oldR, oldCfg := appRedis, appCfg
	appRedis, appCfg = nil, internal.LoadConfig()
	defer func() { appRedis, appCfg = oldR, oldCfg }()

	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles?page=1&limit=10", db, true, nil)

	m.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles v WHERE v\.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).
		WithArgs(10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "imei", "plate_number", "device_model", "status",
			"last_seen_at", "current_lat", "current_lon", "current_speed",
		}).AddRow(uint64(42), "864000000041234", "B 1234 XYZ", "GT06", "active",
			nil, nil, nil, nil))

	vehiclesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "864000000041234") {
		t.Errorf("response missing imei: %s", rec.Body.String())
	}
}

func TestVehiclesListHandler_AdminCountError(t *testing.T) {
	oldR, oldCfg := appRedis, appCfg
	appRedis, appCfg = nil, internal.LoadConfig()
	defer func() { appRedis, appCfg = oldR, oldCfg }()

	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles", db, true, nil)

	m.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles v WHERE v\.deleted_at IS NULL`).
		WillReturnError(errors.New("db down"))

	vehiclesListHandler(c)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// vehicleDetailHandler — non-admin denied + bad id
// ---------------------------------------------------------------------------

func TestVehicleDetailHandler_NonAdminDenied(t *testing.T) {
	oldR, oldCfg := appRedis, appCfg
	appRedis, appCfg = nil, internal.LoadConfig()
	defer func() { appRedis, appCfg = oldR, oldCfg }()

	db, m := mockDB(t)
	// Operator cannot access vehicle 42 (allowed {1}).
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles/42", db, false, map[uint64]struct{}{1: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}

	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "imei", "plate_number", "device_model", "status",
			"last_seen_at", "current_lat", "current_lon", "current_speed",
		}).AddRow(uint64(42), "864000000041234", "B 1234 XYZ", "GT06", "active",
			nil, nil, nil, nil))

	vehicleDetailHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleDetailHandler_BadID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles/abc", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}

	vehicleDetailHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// writeSuccess envelope sanity with pagination
// ---------------------------------------------------------------------------

func TestWriteSuccessPaginationEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	writeSuccess(c, http.StatusOK, []string{"a"}, &models.PaginationInfo{Page: 1, Limit: 10, Total: 1})
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	pag, ok := body["pagination"].(map[string]interface{})
	if !ok || pag["total"] != float64(1) {
		t.Errorf("pagination envelope missing: %v", body["pagination"])
	}
}
