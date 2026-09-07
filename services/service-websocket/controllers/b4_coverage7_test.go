package controllers

// b4_coverage7_test.go (B4 coverage 2026-09-05 — Stage E):
// requireAuth middleware (semua jalur: missing/invalid token, revoke jti,
// authorize error, cross-tenant, platform scope, success), authorize lanjutan
// (company db error, inactive, admin/non-admin), authRefreshHandler dan
// authLogoutHandler flow penuh via miniredis + tokenauth.

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ajb_gps/internal"
	"ajb_gps/internal/tenant"
	"ajb_gps/internal/tokenauth"
	"ajb_gps/service-websocket/models"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
)

// miniredisClient boots an in-memory Redis + client for token tests.
func miniredisClient(t *testing.T) (*miniredis.Miniredis, *internal.RedisClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	return mr, rc
}

// sqlmockRowLCA builds a user_company_access row (id, role_override, is_active).
func sqlmockRowLCA(id int64, override string, active bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "role_override", "is_active"}).AddRow(id, override, active)
}

// sqlmockNewRowsInt builds a single-column row set of int64 values.
func sqlmockNewRowsInt(col string, vals ...int64) *sqlmock.Rows {
	rows := sqlmock.NewRows([]string{col})
	for _, v := range vals {
		rows.AddRow(v)
	}
	return rows
}

// ---------------------------------------------------------------------------
// requireAuth — missing/invalid token
// ---------------------------------------------------------------------------

func TestRequireAuth_MissingToken(t *testing.T) {
	oldCfg := appCfg
	appCfg = internal.LoadConfig()
	appCfg.JWT.RevocationEnabled = true
	defer func() { appCfg = oldCfg }()

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_InvalidToken(t *testing.T) {
	oldCfg := appCfg
	appCfg = internal.LoadConfig()
	appCfg.JWT.RevocationEnabled = true
	defer func() { appCfg = oldCfg }()

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer not.a.jwt")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// requireAuth — revoke jti pada denylist (miniredis)
// ---------------------------------------------------------------------------

func TestRequireAuth_RevokedJTI(t *testing.T) {
	oldCfg := appCfg
	oldRedis := appRedis
	oldMgr := tokenMgr
	oldTenant := appTenant
	defer func() {
		appCfg = oldCfg
		appRedis = oldRedis
		tokenMgr = oldMgr
		appTenant = oldTenant
	}()

	cfg := internal.LoadConfig()
	cfg.JWT.RevocationEnabled = true
	cfg.Redis.KeyPrefix = "adatrack_gps:"
	appCfg = cfg
	appTenant = nil

	mr, rc := miniredisClient(t)
	appRedis = rc
	defer func() { appRedis = oldRedis }()
	tokenMgr = nil

	// Sign a valid token.
	tok, _, err := signToken(cfg, models.MasterUser{ID: 1, CompanyCode: "DEV001", Email: "a@b.io"}, "Operator", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseToken(cfg, tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Deny its jti.
	if err := getTokenManager().DenyJTI(t.Context(), claims.RegisteredClaims.ID, time.Minute); err != nil {
		t.Fatalf("deny: %v", err)
	}

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 TOKEN_REVOKED, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "TOKEN_REVOKED") {
		t.Errorf("expected TOKEN_REVOKED body, got %s", w.Body.String())
	}
	_ = mr
}

func TestRequireAuth_SuccessNonAdmin(t *testing.T) {
	f := newAuthFixture(t)
	appCfg.JWT.RevocationEnabled = true
	tokenMgr = nil

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmockRowLCA(0, "", true))
	f.m.ExpectQuery(`SELECT vehicle_id FROM user_vehicles`).WithArgs(uint64(7)).
		WillReturnRows(sqlmockNewRowsInt("vehicle_id", 42, 43))

	tok, _, err := signToken(appCfg, models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}, "Operator", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) {
		u, ok := loadAuthUser(c)
		if !ok || u.ID != 7 || u.Role != "Operator" {
			t.Errorf("unexpected auth user: %+v ok=%v", u, ok)
		}
		if isAdmin(c) {
			t.Error("operator should not be admin")
		}
		allowed := accessibleVehicleIDsFromCtx(c)
		if len(allowed) != 2 {
			t.Errorf("expected 2 allowed, got %v", allowed)
		}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequireAuth_SuccessAdmin(t *testing.T) {
	f := newAuthFixture(t)
	appCfg.JWT.RevocationEnabled = true
	tokenMgr = nil

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmockRowLCA(0, "Admin", true))

	tok, _, err := signToken(appCfg, models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}, "Operator", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) {
		if !isAdmin(c) {
			t.Error("role_override Admin should set admin flag")
		}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequireAuth_CrossTenant(t *testing.T) {
	oldCfg := appCfg
	oldTenant := appTenant
	oldRedis := appRedis
	oldMgr := tokenMgr
	oldByCode := companyDBByCodeFn
	appCfg = internal.LoadConfig()
	appCfg.JWT.RevocationEnabled = true
	appTenant = nil
	mr, rc := miniredisClient(t)
	appRedis = rc
	tokenMgr = nil
	companyDBByCodeFn = func(string) (*sql.DB, error) { return nil, tenant.ErrCompanyNotFound }
	defer func() {
		appCfg = oldCfg
		appTenant = oldTenant
		appRedis = oldRedis
		tokenMgr = oldMgr
		companyDBByCodeFn = oldByCode
		_ = mr
	}()

	tok, _, err := signToken(appCfg, models.MasterUser{ID: 7, CompanyCode: "ZZZZ", Email: "u@z.io"}, "Operator", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// helpers local (nama unik supaya tidak bentrok dengan file test lain)
// ---------------------------------------------------------------------------

func miniRedisClient(t *testing.T) (rc *internal.RedisClient) {
	t.Helper()
	mr := startMiniredis(t)
	cfg := internal.LoadConfig()
	cfg.Redis.Addr = mr.Addr()
	var err error
	rc, err = internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	return rc
}

// lcaRowStub builds a user_company_access row (id, role_override, is_active).
func lcaRowStub(id uint64, override string, active bool) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
		AddRow(id, override, active)
}

// intVehicleRows returns vehicle_id rows.
func intVehicleRows(ids ...uint64) *sqlmock.Rows {
	r := sqlmock.NewRows([]string{"vehicle_id"})
	for _, id := range ids {
		r = r.AddRow(id)
	}
	return r
}

// tokenauthPayload builds a tokenauth payload for issue/refresh tests.
func tokenauthPayload(uid uint64, company, email, role string) tokenauth.Payload {
	return tokenauth.Payload{
		UserID:      uid,
		CompanyCode: company,
		Email:       email,
		Role:        role,
	}
}

// ---------------------------------------------------------------------------
// authorize — jalur lanjutan
// ---------------------------------------------------------------------------

func TestAuthorize_CompanyDBError(t *testing.T) {
	oldByCode := companyDBByCodeFn
	companyDBByCodeFn = func(string) (*sql.DB, error) { return nil, errors.New("no pool") }
	defer func() { companyDBByCodeFn = oldByCode }()

	if _, _, _, _, _, err := authorize(&tokenClaims{CompanyCode: "DEV001", Role: "Admin", UserID: 1}); err == nil {
		t.Error("expected error for company db failure")
	}
}

func TestAuthorize_Inactive(t *testing.T) {
	f := newAuthFixture(t)
	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }
	// LCA di-resolve dari COMPANY DB (f.db ↔ f.m), bukan master.
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(lcaRowStub(0, "", false))

	if _, _, _, _, _, err := authorize(&tokenClaims{CompanyCode: "DEV001", Role: "Operator", UserID: 7}); err == nil {
		t.Error("expected error for inactive company access")
	}
}

func TestAuthorize_NonAdminWithVehicles(t *testing.T) {
	f := newAuthFixture(t)
	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }
	// Urutan query authorize pada company DB: LCA dulu, lalu user_vehicles.
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(lcaRowStub(0, "", true))
	f.m.ExpectQuery(`SELECT vehicle_id FROM user_vehicles`).WithArgs(uint64(7)).
		WillReturnRows(intVehicleRows(42, 43, 42))

	allowed, db, _, role, admin, err := authorize(&tokenClaims{CompanyCode: "DEV001", Role: "Operator", UserID: 7})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if admin {
		t.Error("operator should not be admin")
	}
	if role != "Operator" {
		t.Errorf("role = %s", role)
	}
	if len(allowed) != 2 || db == nil {
		t.Errorf("unexpected allowed=%v db=%v", allowed, db)
	}
}

func TestAuthorize_AdminNilAllowed(t *testing.T) {
	f := newAuthFixture(t)
	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }
	// Admin: LCA di company DB; user_vehicles tidak di-query (allowed nil).
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(lcaRowStub(0, "Admin", true))

	allowed, _, _, role, admin, err := authorize(&tokenClaims{CompanyCode: "DEV001", Role: "Admin", UserID: 7})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if !admin || role != "Admin" {
		t.Errorf("expected admin, role=%s admin=%v", role, admin)
	}
	if len(allowed) != 0 {
		t.Errorf("admin allowed should be empty, got %v", allowed)
	}
}
func TestRequireAuth_PlatformScope(t *testing.T) {
	oldCfg := appCfg
	oldTenant := appTenant
	oldRedis := appRedis
	oldMgr := tokenMgr
	appCfg = internal.LoadConfig()
	appCfg.JWT.RevocationEnabled = true
	appTenant = nil
	// Revocation check butuh Redis (getTokenManager panic bila appRedis nil).
	mr, rc := miniredisClient(t)
	_ = mr
	appRedis = rc
	tokenMgr = nil
	defer func() {
		appCfg = oldCfg
		appTenant = oldTenant
		appRedis = oldRedis
		tokenMgr = oldMgr
	}()

	tok, _, err := signToken(appCfg, models.MasterUser{ID: 1, CompanyCode: "default", Email: "p@io"}, "SuperAdmin", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	r := gin.New()
	r.GET("/api/v1/vehicles", requireAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 PLATFORM_SCOPE, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// authRefreshHandler
// ---------------------------------------------------------------------------

func htokFixture(t *testing.T) (*internal.Config, *internal.RedisClient) {
	t.Helper()
	oldCfg := appCfg
	oldRedis := appRedis
	oldMgr := tokenMgr
	cfg := internal.LoadConfig()
	cfg.JWT.RefreshExpiry = 24 * time.Hour
	cfg.Redis.KeyPrefix = "adatrack_gps:"
	appCfg = cfg
	_, rc := miniredisClient(t)
	appRedis = rc
	tokenMgr = nil
	t.Cleanup(func() {
		appCfg = oldCfg
		appRedis = oldRedis
		tokenMgr = oldMgr
	})
	return cfg, rc
}

func TestAuthRefreshHandler_BadBody(t *testing.T) {
	htokFixture(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":""}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthRefreshHandler_InvalidToken(t *testing.T) {
	htokFixture(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"garbage"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthRefreshHandler_Success(t *testing.T) {
	cfg, _ := htokFixture(t)
	gin.SetMode(gin.TestMode)

	rt, err := getTokenManager().IssueRefresh(t.Context(), tokenauthPayload(7, "DEV001", "a@b.io", "Operator"),
		cfg.JWT.RefreshExpiry)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh",
		strings.NewReader(`{"refresh_token":"`+rt+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authRefreshHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"refresh_token"`) || !strings.Contains(w.Body.String(), `"token"`) {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// authLogoutHandler
// ---------------------------------------------------------------------------

func TestAuthLogoutHandler_Empty(t *testing.T) {
	htokFixture(t)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authLogoutHandler(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthLogoutHandler_RefreshOnly(t *testing.T) {
	cfg, _ := htokFixture(t)
	gin.SetMode(gin.TestMode)

	rt, err := getTokenManager().IssueRefresh(t.Context(), tokenauthPayload(7, "DEV001", "a@b.io", "Operator"),
		cfg.JWT.RefreshExpiry)
	if err != nil {
		t.Fatalf("IssueRefresh: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout",
		strings.NewReader(`{"refresh_token":"`+rt+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	authLogoutHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Refresh harus hangus.
	if _, err := getTokenManager().ResolveRefresh(t.Context(), rt); err == nil {
		t.Error("expected refresh to be revoked after logout")
	}
}

func TestAuthLogoutHandler_WithBearer(t *testing.T) {
	cfg, _ := htokFixture(t)
	gin.SetMode(gin.TestMode)

	access, _, err := signToken(cfg, models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "a@b.io"}, "Operator", nil)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := parseToken(cfg, access)
	if err != nil || claims.RegisteredClaims.ID == "" {
		t.Fatalf("parse token: %v jti=%q", err, claims.RegisteredClaims.ID)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Authorization", "Bearer "+access)

	authLogoutHandler(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	denied, derr := getTokenManager().IsJTIDenied(t.Context(), claims.RegisteredClaims.ID)
	if derr != nil || !denied {
		t.Fatalf("expected jti denied, denied=%v err=%v", denied, derr)
	}
}
