package controllers

// b4_coverage8_test.go (B4 coverage 2026-09-05 — Stage F):
// registry.lookup negative-cache + fetch error path, companyRead fallback /
// companyDB invalid ctx, vehicleDetailHandler success, geofenceAccessible
// (admin/empty/db), userCreateHandler guard branches.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// registry.lookup — negative cache + fetch error path (appTenant nil)
// ---------------------------------------------------------------------------

func TestRegistryLookupNegativeCache(t *testing.T) {
	oldTenant := appTenant
	appTenant = nil // fetch → companyReadByCode error → negative cache
	defer func() { appTenant = oldTenant }()

	r := newVehicleRegistry()
	if _, ok := r.lookup("DEV001", "unknown"); ok {
		t.Error("unknown IMEI should not resolve")
	}
	// Second call within 30s hits the negative cache (info.ID == 0).
	info, ok := r.lookup("DEV001", "unknown")
	if !ok || info.ID != 0 {
		t.Errorf("expected cached negative entry (ID 0), got ok=%v info=%+v", ok, info)
	}
}

func TestRegistryLookupCacheHit(t *testing.T) {
	r := newVehicleRegistry()
	r.cache[registryKey("DEV001", "111")] = registryEntry{
		info:   vehicleInfo{ID: 42, Model: "GT06", Plate: "B 1234 XYZ"},
		expire: time.Now().Add(time.Minute),
	}
	info, ok := r.lookup("DEV001", "111")
	if !ok || info.ID != 42 || info.Model != "GT06" || info.Plate != "B 1234 XYZ" {
		t.Errorf("cache hit mismatch: ok=%v info=%+v", ok, info)
	}
}

// ---------------------------------------------------------------------------
// companyRead fallback + companyDB invalid context
// ---------------------------------------------------------------------------

func TestCompanyReadFallsBackToPrimary(t *testing.T) {
	db, _ := mockDB(t)
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Set(ctxCompanyDBKey, db) // no ctxCompanyROKey → fallback primary

	got, err := companyRead(c)
	if err != nil || got != db {
		t.Errorf("companyRead fallback = %v, %v", got, err)
	}
}

func TestCompanyDBInvalidContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	if _, err := companyDB(c); err == nil {
		t.Error("expected error without company DB ctx")
	}
	c.Set(ctxCompanyDBKey, "not-a-db")
	if _, err := companyDB(c); err == nil {
		t.Error("expected error for invalid ctx type")
	}
}

// ---------------------------------------------------------------------------
// vehicleDetailHandler — success (admin bypass + enrich)
// ---------------------------------------------------------------------------

func TestVehicleDetailHandler_Success(t *testing.T) {
	// enrichVehicles butuh appRedis → miniredis (tanpa entry → fallback ke DB row).
	mr := startMiniredis(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	red, rerr := internal.NewRedisClient(cfg, nil, nil)
	if rerr != nil {
		t.Fatalf("NewRedisClient: %v", rerr)
	}
	oldCfg, oldRedis := appCfg, appRedis
	appCfg, appRedis = cfg, red
	defer func() { appCfg, appRedis = oldCfg, oldRedis }()

	db, m := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodGet, "/api/v1/vehicles/42", db, true, nil)
	c.Params = gin.Params{{Key: "id", Value: "42"}}

	m.ExpectQuery(`FROM vehicles v WHERE v\.id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "imei", "plate_number", "device_model", "status",
			"last_seen_at", "current_lat", "current_lon", "current_speed",
		}).AddRow(uint64(42), "864000000041234", "B 1234 XYZ", "GT06", "ACTIVE",
			time.Now(), -6.2, 106.8, 12.5))

	vehicleDetailHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "864000000041234") {
		t.Errorf("response missing imei: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// geofenceAccessible — admin / empty allowed / DB count>0 / DB count=0
// ---------------------------------------------------------------------------

func TestGeofenceAccessiblePaths(t *testing.T) {
	g := &geofenceModel{ID: 5, Name: "GF", AreaType: "circle"}

	// Admin → selalu boleh (tanpa DB).
	cAdmin, _ := ginCtx(http.MethodGet, "/x")
	cAdmin.Set(ctxAdminKey, true)
	if !geofenceAccessible(cAdmin, nil, g) {
		t.Error("admin should access any geofence")
	}

	// Non-admin + set kosong → ditolak tanpa sentuh DB.
	dbEmpty, _ := mockDB(t)
	cEmpty, _ := ginCtxWithDB(http.MethodGet, "/x", dbEmpty, false, map[uint64]struct{}{})
	if geofenceAccessible(cEmpty, map[uint64]struct{}{}, g) {
		t.Error("empty allowed set should be denied")
	}

	// DB: ada link vehicle → boleh.
	dbOK, mOK := mockDB(t)
	cOK, _ := ginCtxWithDB(http.MethodGet, "/x", dbOK, false, map[uint64]struct{}{42: {}})
	mOK.ExpectQuery(`SELECT COUNT\(\*\) FROM geofence_vehicles`).
		WithArgs(uint64(5), uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(1)))
	if !geofenceAccessible(cOK, map[uint64]struct{}{42: {}}, g) {
		t.Error("linked geofence should be accessible")
	}

	// DB: tidak ada link → ditolak.
	dbNo, mNo := mockDB(t)
	cNo, _ := ginCtxWithDB(http.MethodGet, "/x", dbNo, false, map[uint64]struct{}{42: {}})
	mNo.ExpectQuery(`SELECT COUNT\(\*\) FROM geofence_vehicles`).
		WithArgs(uint64(5), uint64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(int64(0)))
	if geofenceAccessible(cNo, map[uint64]struct{}{42: {}}, g) {
		t.Error("unlinked geofence should be denied")
	}
}

// ---------------------------------------------------------------------------
// userCreateHandler — guard validasi murni (platform-only endpoint)
// ---------------------------------------------------------------------------

func platformUserCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", nil)
	c.Set(ctxUserKey, models.AuthUser{ID: 1, CompanyCode: "default", Role: "SuperAdmin"})
	c.Set(ctxAdminKey, true)
	return c, rec
}

func postUserJSON(c *gin.Context, body string) {
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
}

func TestUserCreateHandler_PlatformOnly(t *testing.T) {
	db, _ := mockDB(t)
	c, rec := ginCtxWithDB(http.MethodPost, "/api/v1/users", db, false, nil)
	postUserJSON(c, `{"company_code":"DEV001","email":"a@b.com","password":"longenough1","full_name":"X"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 PLATFORM_ONLY, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_PlatformRoleReserved(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"DEV001","email":"a@b.com","password":"longenough1","full_name":"X","role":"superadmin"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 PLATFORM_ROLE_RESERVED, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_RequiredFields(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"DEV001","email":"","password":"longenough1","full_name":"X"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_InvalidEmail(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"DEV001","email":"notanemail","password":"longenough1","full_name":"X"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 email, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_WeakPassword(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"DEV001","email":"a@b.com","password":"short","full_name":"X"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 WEAK_PASSWORD, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_InvalidRole(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"DEV001","email":"a@b.com","password":"longenough1","full_name":"X","role":"wizard"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 role, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestUserCreateHandler_PlatformCompanyRejected(t *testing.T) {
	c, rec := platformUserCtx()
	postUserJSON(c, `{"company_code":"default","email":"a@b.com","password":"longenough1","full_name":"X"}`)
	userCreateHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 platform company, got %d: %s", rec.Code, rec.Body.String())
	}
}
