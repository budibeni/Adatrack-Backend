package controllers

// coverage_auth_test.go (B4 2026-09-08): authLoginHandler + refresh/logout
// handler paths, based on sqlmock master DB + fake redis token manager.

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"ajb_gps/internal"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	gin.SetMode(gin.TestMode)
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

// genBcryptHash produces a real bcrypt hash for the given password (cost 6 —
// fast enough for tests, still valid for CompareHashAndPassword).
func genBcryptHash(pw string, t *testing.T) string {
	hb, err := bcrypt.GenerateFromPassword([]byte(pw), 6)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	return string(hb)
}

func freshLoginLimiter() {
	if appCfg == nil {
		appCfg = newTestCfg()
	}
	appCfg.RateLimit.LoginMaxAttempts = 5
	appCfg.RateLimit.LoginWindow = 15 * time.Minute
	loginLimiter = newFailureRateLimiter(5, 15*time.Minute)
}

// ---------------------------------------------------------------------
// POST /auth/login — authLoginHandler
// ---------------------------------------------------------------------

func TestLoginBadJSON(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", "not-json")

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("login bad json = %d, want 400", code)
	}
}

func TestLoginEmptyEmail(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"  ","password":"x"}`)

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("login empty = %d, want 400", code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@x.io","password":"pw"}`)
	for i := 0; i < 5; i++ {
		loginLimiter.recordFailure("192.0.2.1") // = c.ClientIP() (tanpa port)
	}

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusTooManyRequests {
		t.Fatalf("login rate limited = %d, want 429", code)
	}
}

func TestLoginUnknownUser(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@x.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@x.io").WillReturnRows(sqlmock.NewRows(avMasterUserCols))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusUnauthorized {
		t.Fatalf("login unknown = %d, want 401", code)
	}
}

func TestLoginPlatformSuperAdminSuccess(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	_ = installFakeTokenManager(t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"plat@x.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	hash := genBcryptHash("pw", t)

	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("plat@x.io").
		WillReturnRows(avMasterUserRow(hash, "SuperAdmin", "active", "default", nil))
	mm.ExpectExec("(?i)UPDATE users SET last_login").
		WithArgs(uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusOK {
		t.Fatalf("login platform = %d, want 200", code)
	}
	if err := mm.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestLoginPlatformNonSuperAdminForbidden(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@x.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)
	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@x.io").
		WillReturnRows(avMasterUserRow("bogus", "Operator", "active", "default", nil))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login platform non-super = %d, want 403", code)
	}
}

// stubLoginTenantDB menyiapkan company DB mock utk jalur login tenant
// (companyDBByCodeFn → loadCompanyAccess). Master user id = 7 (avMasterUserRow).
func stubLoginTenantDB(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).
			AddRow(1, 7, nil, true))
	return cm
}

func TestLoginBadPassword2(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"locked@x.io","password":"x"}`)
	_, mm, _ := stubMasterDB(t)
	locked := time.Now().Add(time.Hour)

	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("locked@x.io").
		WillReturnRows(avMasterUserRow("bogus", "Admin", "active", "DEV001", locked))
	stubLoginTenantDB(t)

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login locked = %d, want 403", code)
	}
}

func TestLoginInactiveAccount(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"in@x.io","password":"x"}`)
	_, mm, _ := stubMasterDB(t)

	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("in@x.io").
		WillReturnRows(avMasterUserRow("bogus", "Admin", "disabled", "DEV001", nil))
	stubLoginTenantDB(t)

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login inactive = %d, want 403", code)
	}
}

func TestLoginNoCompanyAccess(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"na@x.io","password":"x"}`)
	_, mm, _ := stubMasterDB(t)

	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("na@x.io").
		WillReturnRows(avMasterUserRow("bogus", "Admin", "active", "DEV001", nil))
	cdb, cm := mockDB(t)
	stubDBByCode(t, cdb, nil)
	cm.ExpectQuery("user_company_access").
		WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusForbidden {
		t.Fatalf("login no-access = %d, want 403", code)
	}
}

func TestLoginMasterQueryError(t *testing.T) {
	t.Helper()
	freshLoginLimiter()
	c, _, _ := companyCtx(t, true, "POST", "/auth/login", `{"email":"u@x.io","password":"pw"}`)
	_, mm, _ := stubMasterDB(t)

	mm.ExpectQuery("SELECT id, company_id, company_code, email, password_hash").
		WithArgs("u@x.io").WillReturnError(errors.New("boom"))

	authLoginHandler(c)

	if code := c.Writer.Status(); code != http.StatusServiceUnavailable {
		t.Fatalf("login master err = %d, want 503", code)
	}
}

// ---------------------------------------------------------------------
// POST /auth/refresh — authRefreshHandler
// ---------------------------------------------------------------------

func TestRefreshBadBody(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/refresh", "not-json")

	authRefreshHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("refresh bad body = %d, want 400", code)
	}
}

func TestRefreshInvalidToken(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/refresh", `{"refresh_token":"not-a-token"}`)

	authRefreshHandler(c)

	if code := c.Writer.Status(); code != http.StatusUnauthorized {
		t.Fatalf("refresh invalid = %d, want 401", code)
	}
}

// ---------------------------------------------------------------------
// POST /auth/logout — authLogoutHandler
// ---------------------------------------------------------------------

func TestLogoutNoEffect(t *testing.T) {
	t.Helper()
	_ = installFakeTokenManager(t)
	c, _, _ := companyCtx(t, true, "POST", "/auth/logout", `{}`)

	authLogoutHandler(c)

	if code := c.Writer.Status(); code != http.StatusBadRequest {
		t.Fatalf("logout no-effect = %d, want 400", code)
	}
}

// ---------------------------------------------------------------------
// base.go small helpers — Init/Shutdown/DB resolution/auditDB
// ---------------------------------------------------------------------

func TestBaseInitShutdown(t *testing.T) {
	t.Helper()
	c, _, m := companyCtx(t, true, "GET", "/vehicles", "")
	_ = m

	// masterDB(): appTenant nil sebelum Init → aman (guard). Init dengan nil
	// tidak crash; Shutdown no-op bila appTenant nil.
	Init(nil, nil, nil, nil)
	if got := masterDB(); got != nil {
		t.Fatal("masterDB should be nil with nil manager")
	}
	if got, err := companyDBByCode("DEV001"); got != nil || err == nil {
		t.Fatalf("companyDBByCode err case mismatch: db=%v err=%v", got, err)
	}
	if got, err := companyReadByCode("DEV001"); got != nil || err == nil {
		t.Fatalf("companyReadByCode err case mismatch: db=%v err=%v", got, err)
	}
	if got := auditDB(); got != nil {
		t.Fatal("auditDB should be nil with nil manager")
	}
	if got, err := companyDB(c); got == nil || err != nil {
		t.Fatalf("companyDB should resolve from ctx: db=%v err=%v", got, err)
	}
	Shutdown()
}