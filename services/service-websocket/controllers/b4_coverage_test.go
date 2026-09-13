package controllers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// B4: unit tests berbasis sqlmock untuk service-websocket controllers.
// Handler sukses-path butuh appTenant (private pool) — fokus pada DB-helpers
// yang menerima *sql.DB + middleware/rbac murni (pola worker-alert 80%).
// ---------------------------------------------------------------------------

func mockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, mock
}

func ginCtx(method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, rec
}

// ---------------------------------------------------------------------------
// auth.go — DB helpers
// ---------------------------------------------------------------------------

func TestLoadMasterUserByEmail(t *testing.T) {
	db, m := mockDB(t)

	rows := sqlmock.NewRows([]string{
		"id", "company_id", "company_code", "email", "password_hash",
		"full_name", "username", "first_name", "last_name", "phone_number",
		"email_verified", "phone_verified", "mfa_enabled", "locale", "avatar_url",
		"failed_login_attempts", "password_changed_at", "locked_until",
		"deleted_at", "last_login", "created_by", "updated_by", "role", "status",
	}).AddRow(
		1, int64(7), "DEV001", "admin@dev001.io", "$2a$12$hash",
		"Admin DEV", "admin", "Admin", "DEV", "+628111",
		true, false, false, "id", "", 0,
		nil, nil, nil, nil, int64(1), nil, "Admin", "active",
	)
	m.ExpectQuery(`SELECT id, company_id`).WithArgs("admin@dev001.io").WillReturnRows(rows)

	u, err := loadMasterUserByEmail(db, " admin@dev001.io ")
	if err != nil {
		t.Fatalf("loadMasterUserByEmail: %v", err)
	}
	if u.ID != 1 || u.CompanyCode != "DEV001" || u.Role != "Admin" || !u.EmailVerified {
		t.Errorf("unexpected user: %+v", u)
	}
	if u.CreatedBy == nil || *u.CreatedBy != 1 {
		t.Errorf("expected created_by=1, got %v", u.CreatedBy)
	}

	m.ExpectQuery(`SELECT id, company_id`).WithArgs("x@x.io").WillReturnError(sql.ErrNoRows)
	if _, err := loadMasterUserByEmail(db, "x@x.io"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows, got %v", err)
	}

	m.ExpectQuery(`SELECT id, company_id`).WithArgs("bad@x.io").WillReturnError(errors.New("boom"))
	if _, err := loadMasterUserByEmail(db, "bad@x.io"); err == nil {
		t.Error("expected error on query failure")
	}
}

func TestLoadCompanyAccess(t *testing.T) {
	db, m := mockDB(t)

	m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(uint64(10), "Manager", true))
	id, override, active, err := loadCompanyAccess(db, 1)
	if err != nil || id != 10 || override != "Manager" || !active {
		t.Errorf("loadCompanyAccess = %d,%s,%v,%v", id, override, active, err)
	}

	m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(uint64(11), nil, false))
	id, override, active, err = loadCompanyAccess(db, 2)
	if err != nil || id != 11 || override != "" || active {
		t.Errorf("loadCompanyAccess = %d,%s,%v,%v", id, override, active, err)
	}

	m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(3)).
		WillReturnError(sql.ErrNoRows)
	if _, _, _, err := loadCompanyAccess(db, 3); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestUserVehicleIDs(t *testing.T) {
	db, m := mockDB(t)

	m.ExpectQuery(`SELECT vehicle_id FROM user_vehicles`).WithArgs(uint64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(int64(1)).AddRow(int64(2)).AddRow(int64(1)))
	ids, err := userVehicleIDs(db, 1)
	if err != nil {
		t.Fatalf("userVehicleIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 unique ids, got %v", ids)
	}

	m.ExpectQuery(`SELECT vehicle_id FROM user_vehicles`).WithArgs(uint64(2)).
		WillReturnError(errors.New("query fail"))
	if _, err := userVehicleIDs(db, 2); err == nil {
		t.Error("expected error")
	}
}

func TestRecordFailedLogin(t *testing.T) {
	recordFailedLogin(nil, 1, 3, time.Minute) // nil DB → no-op

	db, m := mockDB(t)
	m.ExpectExec(`UPDATE users SET failed_login_attempts`).WithArgs(3, 60, uint64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	recordFailedLogin(db, 1, 3, time.Minute)
	if err := m.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (success): %v", err)
	}

	recordFailedLogin(db, 1, 0, time.Minute) // threshold 0 → no-op

	m3, mock3 := mockDB(t)
	mock3.ExpectExec(`UPDATE users SET failed_login_attempts`).WithArgs(3, 60, uint64(9)).
		WillReturnError(errors.New("db down"))
	recordFailedLogin(m3, 9, 3, time.Minute) // error → log only, no panic
}

// ---------------------------------------------------------------------------
// handlers_shared.go — helpers murni
// ---------------------------------------------------------------------------

func TestPaginationParamsDefaultsAndCap(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles?page=abc&limit=99999")
	page, limit := paginationParams(c)
	if page != 1 || limit != 500 {
		t.Errorf("expected (1,500), got (%d,%d)", page, limit)
	}
	c2, _ := ginCtx(http.MethodGet, "/api/v1/vehicles?page=0&limit=0")
	if p, l := paginationParams(c2); p != 1 || l != 100 {
		t.Errorf("expected (1,100), got (%d,%d)", p, l)
	}
	c3, _ := ginCtx(http.MethodGet, "/api/v1/vehicles?page=2&limit=25")
	if p, l := paginationParams(c3); p != 2 || l != 25 {
		t.Errorf("expected (2,25), got (%d,%d)", p, l)
	}
}

func TestSmallSharedHelpers(t *testing.T) {
	if got := placeholders(3); got != "?,?,?" {
		t.Errorf("placeholders(3) = %s", got)
	}
	if got := mapKeys(map[uint64]struct{}{5: {}}); len(got) != 1 {
		t.Errorf("mapKeys = %v", got)
	}
	s := sql.NullString{String: "x", Valid: true}
	if got := nullableStrP(s); got == nil || *got != "x" {
		t.Errorf("nullableStrP = %v", got)
	}
	if got := nullableStrP(sql.NullString{Valid: false}); got != nil {
		t.Errorf("nullableStrP invalid = %v", got)
	}
	f := sql.NullFloat64{Float64: 1.5, Valid: true}
	if got := nullableFloat(f); got == nil || *got != 1.5 {
		t.Errorf("nullableFloat = %v", got)
	}
	if got := nullableFloat(sql.NullFloat64{Valid: false}); got != nil {
		t.Errorf("nullableFloat invalid = %v", got)
	}
	tm := time.Now()
	nt := sql.NullTime{Time: tm, Valid: true}
	if got := nullableTimeP(nt); got == nil || !got.Equal(tm) {
		t.Errorf("nullableTimeP = %v", got)
	}
	ui := sql.NullInt64{Int64: 77, Valid: true}
	if got := nullableUint(ui); got == nil || *got != 77 {
		t.Errorf("nullableUint = %v", got)
	}
	if got := nullableStr("  "); got != nil {
		t.Errorf("nullableStr blank = %v", got)
	}
	if got := nullableStr("abc"); got != "abc" {
		t.Errorf("nullableStr value = %v", got)
	}
	if atoiDefault("42", 0) != 42 || atoiDefault("zz", 7) != 7 || atoiDefault("", 8) != 8 {
		t.Error("atoiDefault edge")
	}
}

// ---------------------------------------------------------------------------
// middleware.go — CORS / API rate limiter / security headers
// ---------------------------------------------------------------------------

func TestOriginAllowed(t *testing.T) {
	old := appCfg
	appCfg = &internal.Config{}
	appCfg.HTTP.CORSOrigins = []string{"https://app.example.com"}
	defer func() { appCfg = old }()

	if originAllowed("") {
		t.Error("empty origin should be denied")
	}
	if !originAllowed("https://app.example.com") {
		t.Error("allowlisted origin should be allowed")
	}
	if originAllowed("https://evil.example.com") {
		t.Error("non-allowlisted origin should be denied")
	}
	appCfg.HTTP.CORSOrigins = []string{"*"}
	if !originAllowed("https://anything.example.com") {
		t.Error("wildcard should allow any origin")
	}
}

func TestCorsMiddleware(t *testing.T) {
	old := appCfg
	appCfg = &internal.Config{}
	appCfg.HTTP.CORSOrigins = []string{"https://app.example.com"}
	defer func() { appCfg = old }()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.OPTIONS("/x", corsMiddleware(), func(c *gin.Context) {})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://app.example.com")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Errorf("missing ACAO header: %v", w.Header())
	}

	r.GET("/y", corsMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/y", nil)
	req2.Header.Set("Origin", "https://evil.example.com")
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Code)
	}
	if w2.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("unexpected ACAO header: %v", w2.Header())
	}

	appCfg.HTTP.CORSOrigins = []string{"*"}
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/y", nil)
	req3.Header.Set("Origin", "https://x.example.com")
	r.ServeHTTP(w3, req3)
	// Wildcard: originAllowed → true → ACAO echo origin (bukan literal "*").
	if w3.Header().Get("Access-Control-Allow-Origin") != "https://x.example.com" {
		t.Errorf("expected echoed ACAO, got %q", w3.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestAPIRateLimiter(t *testing.T) {
	l := newAPIRateLimiter(3)
	if !l.allow("u1") {
		t.Fatal("first call should be allowed")
	}
	if !l.allow("u1") || !l.allow("u1") {
		t.Fatal("calls within max should be allowed")
	}
	if l.allow("u1") {
		t.Fatal("4th call should be denied")
	}
	if !l.allow("u2") {
		t.Fatal("different key should be allowed")
	}
	now := time.Now()
	l.buckets["u1"] = &apiRateBucket{windowStart: now.Add(-2 * time.Minute), count: 3}
	if !l.allow("u1") {
		t.Fatal("expired window should reset bucket")
	}
}

func TestAPIRateLimitMiddleware(t *testing.T) {
	oldLimiter := apiLimiter
	apiLimiter = newAPIRateLimiter(2)
	defer func() { apiLimiter = oldLimiter }()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(ctxUserKey, models.AuthUser{ID: 7})
		c.Next()
	})
	r.GET("/api/v1/vehicles", apiRateLimitMiddleware(), func(c *gin.Context) { c.Status(http.StatusOK) })

	var last int
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
		r.ServeHTTP(w, req)
		last = w.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 3rd request, got %d", last)
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	r := gin.New()
	r.Use(securityHeadersMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Content-Type-Options") == "" {
		t.Error("expected security headers set")
	}
}

// ---------------------------------------------------------------------------
// rbac.go — helpers murni + authorize gate paths (platform tier, tanpa DB)
// ---------------------------------------------------------------------------

func TestRBACHelpers(t *testing.T) {
	if !isAdminRole("Admin") || !isAdminRole("ADMIN") || isAdminRole("Manager") {
		t.Error("isAdminRole case-insensitive")
	}

	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles")
	if isAdmin(c) {
		t.Error("isAdmin default should be false")
	}
	c.Set(ctxAdminKey, true)
	if !isAdmin(c) {
		t.Error("isAdmin true expected")
	}
	c.Set(ctxUserKey, models.AuthUser{CompanyCode: "default", Role: "SuperAdmin"})
	if !isPlatformAdmin(c) {
		t.Error("platform identity should be platform admin")
	}
	c.Set(ctxUserKey, models.AuthUser{CompanyCode: "DEV001", Role: "Admin"})
	if isPlatformAdmin(c) {
		t.Error("tenant admin is not a platform admin")
	}
	if !isPlatformPath("/api/v1/companies") || !isPlatformPath("/api/v1/users") || isPlatformPath("/api/v1/vehicles") {
		t.Error("isPlatformPath allowlist")
	}
}

func TestRequireVehicleAccess(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles/9")
	c.Set(ctxAdminKey, true)
	if !requireVehicleAccess(c, 9) {
		t.Error("admin can access any vehicle")
	}

	c2, rec := ginCtx(http.MethodGet, "/api/v1/vehicles/9")
	c2.Set(ctxAdminKey, false)
	c2.Set(ctxAllowedKey, map[uint64]struct{}{1: {}})
	c2.Set(ctxUserKey, models.AuthUser{ID: 5, CompanyCode: "DEV001", Role: "Operator"})
	if requireVehicleAccess(c2, 9) {
		t.Error("operator without access should be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestAuthorizePlatformGates(t *testing.T) {
	if _, _, _, _, _, err := authorize(&tokenClaims{CompanyCode: "default", Role: "Admin"}); err == nil {
		t.Error("expected error for non-SuperAdmin platform context")
	}
	allowed, db, _, role, admin, err := authorize(&tokenClaims{CompanyCode: "default", Role: "SuperAdmin", UserID: 1})
	if err != nil || db != nil || admin || role != "SuperAdmin" || len(allowed) != 0 {
		t.Errorf("unexpected platform authorize: allowed=%v db=%v role=%s admin=%v err=%v", allowed, db, role, admin, err)
	}
}

func TestCanAccessVehicleDenies(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/vehicles/3")
	c.Set(ctxAdminKey, false)
	c.Set(ctxAllowedKey, map[uint64]struct{}{1: {}})
	c.Set(ctxUserKey, models.AuthUser{ID: 2, CompanyCode: "DEV001", Role: "Driver"})
	if canAccessVehicle(c, 3) {
		t.Fatal("vehicle 3 is not in allowed set")
	}

	c2, _ := ginCtx(http.MethodGet, "/api/v1/vehicles/3")
	c2.Set(ctxAdminKey, true)
	if !canAccessVehicle(c2, 3) {
		t.Fatal("admin can access any vehicle")
	}
}

// ---------------------------------------------------------------------------
// handlers_vehicle.go — count/query helpers (sqlmock)
// ---------------------------------------------------------------------------

func TestCountAccessibleVehicles(t *testing.T) {
	db, m := mockDB(t)

	m.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles v WHERE v.deleted_at IS NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(7)))
	if total, err := countAccessibleVehicles(db, true, nil); err != nil || total != 7 {
		t.Errorf("admin count = %d, %v", total, err)
	}

	if total, err := countAccessibleVehicles(db, false, map[uint64]struct{}{}); err != nil || total != 0 {
		t.Errorf("empty allowed count = %d, %v", total, err)
	}

	// Map iteration order is non-deterministic → matcher AnyArg utk kedua id.
	m.ExpectQuery(`SELECT COUNT\(\*\) FROM vehicles v`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(int64(2)))
	if total, err := countAccessibleVehicles(db, false, map[uint64]struct{}{1: {}, 2: {}}); err != nil || total != 2 {
		t.Errorf("scoped count = %d, %v", total, err)
	}
}

func TestQueryVehicles(t *testing.T) {
	db, m := mockDB(t)

	cols := []string{"id", "imei", "plate_number", "device_model", "status",
		"last_seen_at", "current_latitude", "current_longitude", "current_speed"}
	m.ExpectQuery(`SELECT v\.id, v\.imei`).WithArgs(10, 0).
		WillReturnRows(sqlmock.NewRows(cols).
			AddRow(uint64(1), "864201040512345", "B 1 CD", "GT06", "active",
				time.Now(), -6.2, 106.8, 0.0))
	vs, err := queryVehicles(db, true, nil, 1, 10)
	if err != nil {
		t.Fatalf("queryVehicles: %v", err)
	}
	if len(vs) != 1 || vs[0].PlateNumber != "B 1 CD" {
		t.Errorf("unexpected vehicles: %+v", vs)
	}

	vs, err = queryVehicles(db, false, map[uint64]struct{}{}, 1, 10)
	if err != nil || len(vs) != 0 {
		t.Errorf("expected empty, got %v, %v", vs, err)
	}

	m.ExpectQuery(`SELECT v\.id, v\.imei`).WillReturnError(errors.New("boom"))
	if _, err := queryVehicles(db, true, nil, 1, 10); err == nil {
		t.Error("expected query error")
	}
}

// ---------------------------------------------------------------------------
// handlers_geofence.go — toItem / loadGeofenceByID / geofenceAccessible
// ---------------------------------------------------------------------------

func TestGeofenceToItem(t *testing.T) {
	now := time.Now()
	db, m := mockDB(t)
	m.ExpectQuery(`SELECT vehicle_id FROM geofence_vehicles`).WithArgs(uint64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(uint64(40)).AddRow(uint64(41)))

	g := &geofenceModel{
		ID: 3, Name: "Zone A", AreaType: "circle", Coordinates: []byte(`[106.8,-6.2]`),
		RadiusMeters: sql.NullFloat64{Float64: 500, Valid: true}, CreatedBy: 2,
		IsActive: true, CreatedAt: now,
	}
	item := g.toItem(db)
	if item.ID != 3 || item.AreaType != "circle" || item.RadiusMeters == nil || *item.RadiusMeters != 500 {
		t.Errorf("circle item mismatch: %+v", item)
	}
	if len(item.Vehicles) != 2 {
		t.Errorf("expected 2 vehicle links, got %v", item.Vehicles)
	}

	poly := &geofenceModel{ID: 4, Name: "P", AreaType: "polygon", Coordinates: []byte(`[]`), BoundaryPoints: []byte(`[[1,2],[3,4]]`), CreatedAt: now}
	item2 := poly.toItem(nil) // db nil → tanpa query link
	if item2.RadiusMeters != nil || len(item2.Vehicles) != 0 || len(item2.BoundaryPoints) == 0 {
		t.Errorf("polygon item mismatch: %+v", item2)
	}
}

func TestLoadGeofenceByID(t *testing.T) {
	db, m := mockDB(t)
	now := time.Now()
	m.ExpectQuery(`SELECT id, name, area_type`).WithArgs(uint64(5)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "area_type", "coordinates", "radius_meters", "boundary_points", "created_by", "is_active", "created_at"}).
			AddRow(uint64(5), "Zone", "polygon", []byte(`[]`), nil, []byte(`[[1,2]]`), uint64(1), true, now))
	g, err := loadGeofenceByID(db, 5)
	if err != nil || g.ID != 5 {
		t.Errorf("loadGeofenceByID = %v, %v", g, err)
	}

	m.ExpectQuery(`SELECT id, name, area_type`).WillReturnError(sql.ErrNoRows)
	if _, err := loadGeofenceByID(db, 99); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestGeofenceAccessible(t *testing.T) {
	c, _ := ginCtx(http.MethodGet, "/api/v1/geofences/1")
	c.Set(ctxAdminKey, true)
	if !geofenceAccessible(c, nil, &geofenceModel{}) {
		t.Error("admin sees all geofences")
	}

	c2, _ := ginCtx(http.MethodGet, "/api/v1/geofences/1")
	c2.Set(ctxAdminKey, false)
	if geofenceAccessible(c2, nil, &geofenceModel{}) {
		t.Error("scope empty → denied")
	}

	c3, _ := ginCtx(http.MethodGet, "/api/v1/geofences/1")
	c3.Set(ctxAdminKey, false)
	if geofenceAccessible(c3, map[uint64]struct{}{1: {}}, &geofenceModel{ID: 1}) {
		t.Error("no company DB → denied")
	}
}

// ---------------------------------------------------------------------------
// handlers_alert.go — scanAlertJoined / alertByID
// ---------------------------------------------------------------------------

func TestAlertByID(t *testing.T) {
	cols := []string{
		"id", "vehicle_id", "alert_type", "severity", "description",
		"status", "acknowledged_by", "acknowledged_at", "resolved_at",
		"vehicle_lat", "vehicle_lon", "created_at", "imei",
	}
	db, mock := mockDB(t)
	mock.ExpectQuery(`SELECT .* FROM alerts a`).WithArgs(uint64(9)).
		WillReturnRows(sqlmock.NewRows(cols).AddRow(
			uint64(9), uint64(2), "SOS", "critical", nil, "OPEN",
			nil, nil, nil, nil, nil, time.Now(), "864201040512345"))
	item, err := alertByID(db, 9)
	if err != nil {
		t.Fatalf("alertByID: %v", err)
	}
	if item.ID != 9 || item.AlertType != "SOS" || item.Severity != "critical" {
		t.Errorf("unexpected alert: %+v", item)
	}

	mock.ExpectQuery(`SELECT .* FROM alerts a`).WithArgs(uint64(999)).
		WillReturnRows(sqlmock.NewRows(cols))
	if _, err := alertByID(db, 999); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestScanAlertJoinedDirect(t *testing.T) {
	db, mock := mockDB(t)
	cols := []string{
		"id", "vehicle_id", "alert_type", "severity", "description",
		"status", "acknowledged_by", "acknowledged_at", "resolved_at",
		"vehicle_lat", "vehicle_lon", "created_at", "imei",
	}
	mock.ExpectQuery(`SELECT 1`).WillReturnRows(sqlmock.NewRows(cols).AddRow(
		uint64(1), uint64(2), "GEOFENCE_BREACH", "high", nil,
		"OPEN", nil, nil, nil, nil, nil, time.Now(), "864201040512345"))
	rows, _ := db.Query(`SELECT 1`)
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("no rows")
	}
	item, err := scanAlertJoined(rows)
	if err != nil {
		t.Fatalf("scanAlertJoined: %v", err)
	}
	if item.IMEI != "864201040512345" || item.AlertType != "GEOFENCE_BREACH" {
		t.Errorf("unexpected scan: %+v", item)
	}
}
