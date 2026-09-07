package controllers

// b4_coverage3_test.go (B4 coverage lanjutan 2026-09-04):
// handler alerts/geofence (sqlmock + gin ctx), httpMetricsMiddleware,
// installAuthContext, getTokenManager/issueTokenPair/authRefresh/authLogout
// (miniredis), setupRouter, registry.invalidate, signToken roundtrip.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

func ginCtxWithDB(method, target string, db *sql.DB, admin bool, allowed map[uint64]struct{}) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	c.Set(ctxCompanyDBKey, db)
	c.Set(ctxCompanyROKey, db)
	c.Set(ctxAdminKey, admin)
	c.Set(ctxAllowedKey, allowed)
	c.Set(ctxUserKey, models.AuthUser{ID: 7, CompanyCode: "DEV001", Role: "Operator", CompanyUserID: 9})
	return c, rec
}

func TestHTTPMetricsMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(httpMetricsMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestInstallAuthContext(t *testing.T) {
	oldTenant := appTenant
	appTenant = nil
	defer func() { appTenant = oldTenant }()
	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles")
	db, _ := mockDB(t)
	claims := &tokenClaims{UserID: 1, CompanyCode: "DEV001", Email: "a@b.io"}
	installAuthContext(c, claims, map[uint64]struct{}{42: {}}, db, 9, "Admin", true)
	if v, ok := c.Get(ctxCompanyDBKey); !ok || v.(*sql.DB) != db {
		t.Error("company DB not set")
	}
	if v, ok := c.Get(ctxAdminKey); !ok || v.(bool) != true {
		t.Error("admin flag not set")
	}
	if v, ok := c.Get(ctxCompanyCodeKey); !ok || v.(string) != "DEV001" {
		t.Error("company code not set")
	}
}

func TestRegistryInvalidate(t *testing.T) {
	r := newVehicleRegistry()
	r.cache[registryKey("DEV001", "111")] = registryEntry{info: vehicleInfo{ID: 1}, expire: time.Now().Add(time.Minute)}
	r.invalidate("DEV001", "111")
	if _, ok := r.cache[registryKey("DEV001", "111")]; ok {
		t.Error("entry should be invalidated")
	}
	r.invalidate("DEV001", "nonexistent")
}

func TestAlertsListHandler_Admin(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/alerts?page=1&limit=10", db, true, nil)
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM alerts a JOIN vehicles`).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	m.ExpectQuery(`SELECT a\.id, a\.vehicle_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "vehicle_id", "alert_type", "severity", "description", "status",
			"acknowledged_by", "acknowledged_at", "resolved_at", "vehicle_lat", "vehicle_lon", "created_at", "imei",
		}).AddRow(uint64(5), uint64(42), "SOS", "critical", "help", "OPEN",
			nil, nil, nil, -6.2, 106.8, time.Now(), "864000000041234"))
	alertsListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertsListHandler_OperatorEmpty(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/alerts", db, false, map[uint64]struct{}{})
	alertsListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertsListHandler_OperatorWithVehicles(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/alerts?status=OPEN", db, false, map[uint64]struct{}{42: {}})
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM alerts a JOIN vehicles`).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(0)))
	m.ExpectQuery(`SELECT a\.id, a\.vehicle_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "vehicle_id", "alert_type", "severity", "description", "status",
			"acknowledged_by", "acknowledged_at", "resolved_at", "vehicle_lat", "vehicle_lon", "created_at", "imei",
		}))
	alertsListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertAckHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/alerts/5/acknowledge", db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "5"}}
	m.ExpectQuery(`SELECT vehicle_id FROM alerts WHERE id = ?`).WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(42)))
	m.ExpectExec(`UPDATE alerts SET status = 'acknowledged'`).WithArgs(uint64(9), uint64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectQuery(`SELECT a\.id, a\.vehicle_id`).WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "vehicle_id", "alert_type", "severity", "description", "status",
			"acknowledged_by", "acknowledged_at", "resolved_at", "vehicle_lat", "vehicle_lon", "created_at", "imei",
		}).AddRow(uint64(5), uint64(42), "SOS", "critical", "help", "acknowledged",
			uint64(9), time.Now(), nil, -6.2, 106.8, time.Now(), "864000000041234"))
	alertAckHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertAckHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/alerts/99/acknowledge", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "99"}}
	m.ExpectQuery(`SELECT vehicle_id FROM alerts WHERE id = ?`).WithArgs(uint64(99)).
		WillReturnError(sql.ErrNoRows)
	alertAckHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertAckHandler_AlreadyResolved(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/alerts/7/acknowledge", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "7"}}
	m.ExpectQuery(`SELECT vehicle_id FROM alerts WHERE id = ?`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(10)))
	m.ExpectExec(`UPDATE alerts SET status = 'acknowledged'`).WithArgs(uint64(9), uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	alertAckHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAlertAckHandler_InvalidID(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPatch, "/api/v1/alerts/abc/acknowledge", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "abc"}}
	alertAckHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// geofencesListHandler + geofenceDetailHandler
// ---------------------------------------------------------------------------

func TestGeofencesListHandler_Admin(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences", db, true, nil)

	m.ExpectQuery(`SELECT id, name, area_type, coordinates`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points",
			"created_by", "is_active", "created_at",
		}).AddRow(uint64(1), "GF-A", "circle", []byte(`{"lat":-6.2,"lon":106.8}`),
			sql.NullFloat64{Float64: 100, Valid: true}, nil, uint64(1), true, time.Now()))

	geofencesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofencesListHandler_Operator(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences", db, false, map[uint64]struct{}{42: {}})

	m.ExpectQuery(`SELECT g\.id, name, area_type`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points",
			"created_by", "is_active", "created_at",
		}))

	geofencesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofenceDetailHandler_Found(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences/1", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "1"}}

	m.ExpectQuery(`SELECT id, name, area_type`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points",
			"created_by", "is_active", "created_at",
		}).AddRow(uint64(1), "GF-A", "circle", []byte(`{"lat":-6.2,"lon":106.8}`),
			sql.NullFloat64{Float64: 100, Valid: true}, nil, uint64(1), true, time.Now()))

	geofenceDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofenceDetailHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/geofences/999", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT id, name, area_type`).WithArgs(uint64(999)).
		WillReturnError(sql.ErrNoRows)

	geofenceDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// geofenceDeleteHandler + geofencesCreateHandler
// ---------------------------------------------------------------------------

func TestGeofenceDeleteHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/geofences/3", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	m.ExpectExec(`UPDATE geofences SET is_active = FALSE`).WithArgs(uint64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	geofenceDeleteHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofenceDeleteHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/geofences/88", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "88"}}

	m.ExpectExec(`UPDATE geofences SET is_active = FALSE`).WithArgs(uint64(88)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	geofenceDeleteHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofenceDeleteHandler_NonAdmin(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodDelete, "/api/v1/geofences/3", db, false, nil)
	c.Params = gin.Params{{Key: "id", Value: "3"}}

	geofenceDeleteHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofencesCreateHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/geofences", db, true, map[uint64]struct{}{42: {}})

	body := `{"name":"GF-New","area_type":"circle","coordinates":{"lat":-6.2,"lon":106.8},"radius_meters":150,"vehicle_ids":[42]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/geofences", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	m.ExpectBegin()
	m.ExpectExec(`INSERT INTO geofences`).
		WithArgs("GF-New", "circle", json.RawMessage(`{"lat":-6.2,"lon":106.8}`),
			sql.NullFloat64{Float64: 150, Valid: true}, nil, uint64(9)).
		WillReturnResult(sqlmock.NewResult(10, 1))
	m.ExpectExec(`INSERT IGNORE INTO geofence_vehicles`).WithArgs(int64(10), uint64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()

	geofencesCreateHandler(c)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGeofencesCreateHandler_Validation(t *testing.T) {
	db, _ := mockDB(t)

	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/geofences", db, true, nil)
	body := `{"area_type":"circle","coordinates":{"lat":-6.2},"radius_meters":100}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/geofences", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	geofencesCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no name), got %d", rec.Code)
	}

	c2, rec2 := ginCtxWithDB(http.MethodPost, "/api/v1/geofences", db, true, nil)
	body2 := `{"name":"X","area_type":"hexagon","coordinates":{"lat":-6.2},"radius_meters":100}`
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/geofences", strings.NewReader(body2))
	c2.Request.Header.Set("Content-Type", "application/json")
	geofencesCreateHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (bad area_type), got %d", rec2.Code)
	}

	c3, rec3 := ginCtxWithDB(http.MethodPost, "/api/v1/geofences", db, true, nil)
	body3 := `{"name":"X","area_type":"circle","coordinates":{"lat":-6.2}}`
	c3.Request = httptest.NewRequest(http.MethodPost, "/api/v1/geofences", strings.NewReader(body3))
	c3.Request.Header.Set("Content-Type", "application/json")
	geofencesCreateHandler(c3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no radius), got %d", rec3.Code)
	}
}

func TestGeofencesCreateHandler_NonAdmin(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/geofences", db, false, nil)
	body := `{"name":"GF","area_type":"circle","coordinates":{"lat":-6.2},"radius_meters":100}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/geofences", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	geofencesCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// getTokenManager + newJTI + issueTokenPair (miniredis)
// ---------------------------------------------------------------------------

func TestGetTokenManagerAndNewJTI(t *testing.T) {
	old := tokenMgr
	oldCfg := appCfg
	mr := miniredis.RunT(t)
	defer func() { tokenMgr, appCfg = old, oldCfg }()

	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	appCfg = cfg
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldRedis := appRedis
	appRedis = rc
	defer func() { appRedis = oldRedis }()

	tm := getTokenManager()
	if tm == nil {
		t.Fatal("getTokenManager returned nil")
	}
	if tm2 := getTokenManager(); tm2 != tm {
		t.Error("getTokenManager not singleton")
	}

	jti := newJTI()
	if len(jti) != 32 {
		t.Errorf("newJTI length = %d, want 32", len(jti))
	}
}

func TestIssueTokenPair(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldRedis, oldCfg, oldMgr := appRedis, appCfg, tokenMgr
	appRedis, appCfg, tokenMgr = rc, cfg, nil
	defer func() {
		appRedis, appCfg, tokenMgr = oldRedis, oldCfg, oldMgr
	}()

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "a@b.io"}
	access, expSec, refresh, err := issueTokenPair(t.Context(), mu, "Admin", []int64{1, 2})
	if err != nil {
		t.Fatalf("issueTokenPair: %v", err)
	}
	if access == "" {
		t.Error("empty access token")
	}
	if expSec <= 0 {
		t.Errorf("expSec = %d", expSec)
	}
	if refresh == "" {
		t.Error("empty refresh token")
	}
}

// ---------------------------------------------------------------------------
// authRefreshHandler + authLogoutHandler (miniredis + gin)
// ---------------------------------------------------------------------------

func TestAuthRefreshHandler_InvalidBody(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldR, oldC, oldM := appRedis, appCfg, tokenMgr
	appRedis, appCfg, tokenMgr = rc, cfg, nil
	defer func() { appRedis, appCfg, tokenMgr = oldR, oldC, oldM }()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogoutHandler_NoToken(t *testing.T) {
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldR, oldC, oldM := appRedis, appCfg, tokenMgr
	appRedis, appCfg, tokenMgr = rc, cfg, nil
	defer func() { appRedis, appCfg, tokenMgr = oldR, oldC, oldM }()

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authLogoutHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no token), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// setupRouter
// ---------------------------------------------------------------------------

func TestSetupRouter(t *testing.T) {
	oldCfg, oldLL, oldAL := appCfg, loginLimiter, apiLimiter
	defer func() { appCfg, loginLimiter, apiLimiter = oldCfg, oldLL, oldAL }()

	appCfg = internal.LoadConfig()
	appCfg.RateLimit.LoginMaxAttempts = 5
	appCfg.RateLimit.LoginWindow = 15 * time.Minute
	appCfg.RateLimit.APIMaxPerMinute = 100
	appCfg.HTTP.CORSOrigins = []string{"*"}

	h := setupRouter()
	if h == nil {
		t.Fatal("setupRouter returned nil")
	}
}

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// vehicleDetailHandler + vehicleHistoryHandler + vehiclesListHandler
// ---------------------------------------------------------------------------

func TestVehicleDetailHandler_NotFound(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles/999", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "999"}}

	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).WithArgs(uint64(999)).
		WillReturnError(sql.ErrNoRows)

	vehicleDetailHandler(c)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehicleHistoryHandler_Success(t *testing.T) {
	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet,
		"/api/v1/vehicles/42/history?start=2026-08-01T00:00:00Z&end=2026-08-02T00:00:00Z",
		db, false, map[uint64]struct{}{42: {}})
	c.Params = gin.Params{{Key: "id", Value: "42"}}

	// 1) loadVehicleByID (9 kolom vehicleSelectCols).
	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "imei", "plate_number", "device_model", "status",
			"last_seen_at", "current_latitude", "current_longitude", "current_speed",
		}).AddRow(uint64(42), "864000000041234", "B 1234 XYZ",
			sql.NullString{String: "GT06", Valid: true}, "MOVING",
			time.Now(), sql.NullFloat64{Float64: -6.2, Valid: true},
			sql.NullFloat64{Float64: 106.8, Valid: true},
			sql.NullFloat64{Float64: 35.5, Valid: true}))
	// 2) COUNT history.
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM telemetry_logs WHERE imei = \?`).
		WithArgs("864000000041234", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	// 3) SELECT points (DESC + LIMIT).
	m.ExpectQuery(`SELECT timestamp, latitude, longitude, speed, heading`).
		WithArgs("864000000041234", sqlmock.AnyArg(), sqlmock.AnyArg(), 5000).
		WillReturnRows(sqlmock.NewRows([]string{"timestamp", "latitude", "longitude", "speed", "heading"}).
			AddRow(time.Now(), -6.2, 106.8, 35.5, 90))

	vehicleHistoryHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVehiclesListHandler_Operator(t *testing.T) {
	// miniredis utk enrichVehicles (MGET live state).
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	oldR := appRedis
	appRedis = rc
	defer func() { appRedis = oldR }()

	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles?page=1&limit=10", db, false, map[uint64]struct{}{42: {}})

	// 1) COUNT.
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles v`).WithArgs(uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	// 2) SELECT page.
	m.ExpectQuery(`SELECT v\.id, v\.imei, v\.plate_number`).
		WithArgs(uint64(42), 10, 0).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "imei", "plate_number", "device_model", "status",
			"last_seen_at", "current_latitude", "current_longitude", "current_speed",
		}).AddRow(uint64(42), "864000000041234", "B 1234 XYZ",
			sql.NullString{String: "GT06", Valid: true}, "MOVING",
			time.Now(), sql.NullFloat64{Float64: -6.2, Valid: true},
			sql.NullFloat64{Float64: 106.8, Valid: true},
			sql.NullFloat64{Float64: 35.5, Valid: true}))

	vehiclesListHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// writeError shape
// ---------------------------------------------------------------------------

func TestWriteErrorShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	writeError(c, http.StatusBadRequest, "BAD_REQUEST", "something wrong")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["error_code"] != "BAD_REQUEST" {
		t.Errorf("unexpected error_code: %v", body["error_code"])
	}
}

// companyCreateHandler + userCreateHandler — validation paths
// ---------------------------------------------------------------------------

func TestCompanyCreateHandler_Validation(t *testing.T) {
	oldTenant := appTenant
	appTenant = nil
	defer func() { appTenant = oldTenant }()

	gin.SetMode(gin.TestMode)

	// Non-platform caller → 403 PLATFORM_ONLY (guard sebelum bind JSON).
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/companies", strings.NewReader(`{"code":"NEW1","name":"PT Test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	companyCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (non-platform), got %d", rec.Code)
	}

	// Platform identity + missing company_code → 400.
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/companies", strings.NewReader(`{"name":"PT Test"}`))
	c2.Request.Header.Set("Content-Type", "application/json")
	c2.Set(ctxUserKey, models.AuthUser{ID: 1, CompanyCode: "default", Role: "SuperAdmin"})
	c2.Set(ctxAdminKey, true)
	c2.Set(ctxCompanyCodeKey, "default")
	companyCreateHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no company_code), got %d: %s", rec2.Code, rec2.Body.String())
	}
}

func TestUserCreateHandler_Validation(t *testing.T) {
	oldTenant := appTenant
	appTenant = nil
	defer func() { appTenant = oldTenant }()

	gin.SetMode(gin.TestMode)

	// Non-platform caller → 403 PLATFORM_ONLY.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	userCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (non-platform), got %d", rec.Code)
	}

	// Platform identity + empty body → 400.
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{}`))
	c2.Request.Header.Set("Content-Type", "application/json")
	c2.Set(ctxUserKey, models.AuthUser{ID: 1, CompanyCode: "default", Role: "SuperAdmin"})
	c2.Set(ctxAdminKey, true)
	c2.Set(ctxCompanyCodeKey, "default")
	userCreateHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (empty body), got %d: %s", rec2.Code, rec2.Body.String())
	}
}
