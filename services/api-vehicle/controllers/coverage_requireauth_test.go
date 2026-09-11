package controllers

// coverage_requireauth_test.go (B4 coverage api-vehicle 2026-09-09):
// requireAuth middleware + authorize paths end-to-end dengan gin context
// langsung, sqlmock untuk company DB, dan fake token manager untuk
// revocation checks — tanpa infra nyata.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal/tenant"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
)

func newAuthedCtx(t *testing.T, token string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/vehicles", nil)
	c.Request.RemoteAddr = "192.0.2.1:1234"
	c.Request.Header.Set("Authorization", "Bearer "+token)
	return c, rec
}

func signTestAuth(t *testing.T, mu models.MasterUser, role string) string {
	t.Helper()
	tok, _, err := signTokenClaims(mu, role, nil)
	if err != nil {
		t.Fatalf("signTokenClaims: %v", err)
	}
	return tok
}

func TestRequireAuthMissingToken(t *testing.T) {
	t.Helper()
	old := appCfg
	appCfg = newTestCfg()
	t.Cleanup(func() { appCfg = old })

	c, rec := newAuthedCtx(t, "")
	requireAuth()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rec.Code)
	}
}

func TestRequireAuthInvalidToken(t *testing.T) {
	t.Helper()
	old := appCfg
	appCfg = newTestCfg()
	t.Cleanup(func() { appCfg = old })

	c, rec := newAuthedCtx(t, "not-a-jwt")
	requireAuth()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", rec.Code)
	}
}

func TestRequireAuthPlatformCompany(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 1, CompanyCode: "default", Email: "p@x.io"}
	tok := signTestAuth(t, mu, "SuperAdmin")

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("platform company status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuthCrossTenant(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Admin")

	stubDBByCode(t, nil, tenant.ErrCompanyNotFound)

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuthAuthorizeOtherError(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Admin")

	stubDBByCode(t, nil, errors.New("pool init failed"))

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorize err status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuthCompanyAccessInactive(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Admin")

	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("SELECT id, user_id, role_override, is_active FROM user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).
			AddRow(1, 7, "", false))

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("inactive access status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if err := cm.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRequireAuthSuccessInstallsContext(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Operator")

	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("SELECT id, user_id, role_override, is_active FROM user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).
			AddRow(1, 7, "", true))
	cm.ExpectQuery("SELECT vehicle_id FROM user_vehicles WHERE user_id = \\?").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(1).AddRow(2))

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("auth should pass, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := c.GetString(ctxCompanyCodeKey); got != "DEV001" {
		t.Fatalf("company_code ctx = %q, want DEV001", got)
	}
	if v, _ := c.Get(ctxAdminKey); v != false {
		t.Fatal("operator should not be admin")
	}
	if v, _ := c.Get(ctxRoleKey); v != "Operator" {
		t.Fatalf("role = %v, want Operator", v)
	}
	if err := cm.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRequireAuthRevoked(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)

	mu := models.MasterUser{ID: 7, CompanyCode: "DEV001", Email: "u@dev001.io"}
	tok := signTestAuth(t, mu, "Admin")

	claims, err := parseToken(tok)
	if err != nil {
		t.Fatalf("parseToken: %v", err)
	}
	mgr := getTokenManager()
	rc := httptest.NewRecorder()
	cc, _ := gin.CreateTestContext(rc)
	cc.Request = httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	if err := mgr.DenyJTI(cc.Request.Context(), claims.RegisteredClaims.ID, time.Hour); err != nil {
		t.Fatalf("DenyJTI: %v", err)
	}

	c, rec := newAuthedCtx(t, tok)
	requireAuth()(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}