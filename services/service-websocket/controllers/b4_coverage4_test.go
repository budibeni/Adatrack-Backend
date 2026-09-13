package controllers

// b4_coverage4_test.go (B4 coverage 2026-09-04 — Stage B):
// failureRateLimiter, effectiveRole, authLoginHandler (semua jalur via
// masterDBFn/companyDBByCodeFn indirection + sqlmock), auditLogin nil-safe.

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
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
	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// fixture: swap masterDBFn/companyDBByCodeFn + globals, restore after test.
// ---------------------------------------------------------------------------

type authFixture struct {
	db     *sql.DB
	m      sqlmock.Sqlmock
	master *sql.DB
	tmock  sqlmock.Sqlmock
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	f := &authFixture{}

	f.db, f.m = mockDB(t)
	f.master, f.tmock = mockDB(t)

	oldMaster := masterDBFn
	oldByCode := companyDBByCodeFn
	oldCfg := appCfg
	oldRedis := appRedis
	oldTenant := appTenant
	oldLimiter := loginLimiter
	oldMgr := tokenMgr

	appCfg = internal.LoadConfig()
	appCfg.RateLimit.LoginLockoutThreshold = 3
	appCfg.RateLimit.LoginLockoutWindow = 15 * time.Minute
	appCfg.RateLimit.LoginMaxAttempts = 5
	appCfg.RateLimit.LoginWindow = 15 * time.Minute
	loginLimiter = newFailureRateLimiter(5, 15*time.Minute)
	appTenant = nil // auditDB() → nil (LogAudit nil-safe)
	loginLimiter.reset("192.0.2.1")

	mr := miniredis.RunT(t)
	appCfg.Redis.Addr = mr.Addr()
	rc, err := internal.NewRedisClient(appCfg, nil, nil)
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	appRedis = rc
	tokenMgr = nil // force getTokenManager() to build from miniredis

	masterDBFn = func() *sql.DB { return f.master }
	companyDBByCodeFn = func(string) (*sql.DB, error) { return nil, errors.New("company db not provisioned") }

	t.Cleanup(func() {
		masterDBFn = oldMaster
		companyDBByCodeFn = oldByCode
		appCfg = oldCfg
		appRedis = oldRedis
		appTenant = oldTenant
		loginLimiter = oldLimiter
		tokenMgr = oldMgr
	})
	return f
}

// loginCtx builds a gin test context with a JSON login body.
func loginCtx(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

// masterUserCols adalah daftar kolom SELECT loadMasterUserByEmail (24 kolom).
var masterUserCols = []string{
	"id", "company_id", "company_code", "email", "password_hash", "full_name",
	"username", "first_name", "last_name", "phone_number", "email_verified",
	"phone_verified", "mfa_enabled", "locale", "avatar_url", "failed_login_attempts",
	"password_changed_at", "locked_until", "deleted_at", "last_login",
	"created_by", "updated_by", "role", "status",
}

// masterUserRowValues membangun 24 nilai kolom user master.
func masterUserRowValues(hash, role, status, companyCode string, lockedUntil interface{}) []driver.Value {
	return []driver.Value{
		uint64(7), int64(1), companyCode, "u@dev001.io", hash, "Full Name",
		"", "", "", "", false,
		false, false, "id", "", 0,
		nil, lockedUntil, nil, nil,
		nil, nil, role, status,
	}
}

// masterUserRow returns a 24-column sqlmock row matching loadMasterUserByEmail.
func masterUserRow(hash, role, status, companyCode string) *sqlmock.Rows {
	return sqlmock.NewRows(masterUserCols).
		AddRow(masterUserRowValues(hash, role, status, companyCode, nil)...)
}

// masterUserRowUntil = masterUserRow dgn locked_until custom (kolom ke-18).
func masterUserRowUntil(hash, role, status, companyCode string, lockedUntil interface{}) *sqlmock.Rows {
	return sqlmock.NewRows(masterUserCols).
		AddRow(masterUserRowValues(hash, role, status, companyCode, lockedUntil)...)
}

// ---------------------------------------------------------------------------
// failureRateLimiter + effectiveRole
// ---------------------------------------------------------------------------

func TestFailureRateLimiterWindow(t *testing.T) {
	l := newFailureRateLimiter(2, time.Minute)
	if !l.allow("ip1") {
		t.Fatal("empty state should allow")
	}
	l.recordFailure("ip1") // count=1 → masih di bawah max
	if !l.allow("ip1") {
		t.Fatal("count=1 max=2 should allow")
	}
	l.recordFailure("ip1") // count=2 = max → denied (allow = count < max)
	if l.allow("ip1") {
		t.Fatal("count=2 max=2 should deny")
	}
	l.reset("ip1")
	if !l.allow("ip1") {
		t.Fatal("reset should allow")
	}
	// Expired window → entry deleted.
	l.recordFailure("ip2")
	l.entries["ip2"].firstFail = time.Now().Add(-2 * time.Minute)
	if !l.allow("ip2") {
		t.Fatal("expired window should allow")
	}
	// recordFailure with expired window resets to 1.
	l.recordFailure("ip2")
	if l.entries["ip2"].count != 1 {
		t.Fatalf("expected count reset to 1, got %d", l.entries["ip2"].count)
	}
}

// ---------------------------------------------------------------------------
// authLoginHandler — jalur validasi & error
// ---------------------------------------------------------------------------

const loginBody = `{"email":"u@dev001.io","password":"Secret@123"}`

func TestAuthLogin_BadBody(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	c, rec := loginCtx(t, `{"email":`)
	authLoginHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_MissingFields(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	c, rec := loginCtx(t, `{"email":"   ","password":"x"}`)
	authLoginHandler(c)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (blank email), got %d", rec.Code)
	}

	c2, rec2 := loginCtx(t, `{"email":"u@dev001.io"}`)
	authLoginHandler(c2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no password), got %d", rec2.Code)
	}
}

func TestAuthLogin_RateLimited(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	for i := 0; i < 5; i++ {
		loginLimiter.recordFailure("192.0.2.1")
	}
	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_UnknownUser(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnError(sql.ErrNoRows)

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_MasterQueryError(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnError(errors.New("db down"))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_PlatformNonSuperAdmin(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Admin", "active", "default"))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (platform reserved), got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_CompanyDBUnavailable(t *testing.T) {
	f := newAuthFixture(t)
	_ = f

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Admin", "active", "DEV001"))
	// companyDBByCodeFn (fixture default) returns error → 403.

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_NoCompanyAccess(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Operator", "active", "DEV001"))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnError(sql.ErrNoRows)

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (no company access), got %d: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// authLoginHandler — inactive / locked / bad password / success
// ---------------------------------------------------------------------------

func TestAuthLogin_AccountInactive(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Admin", "suspended", "DEV001"))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(int64(11), "Admin", true))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 ACCOUNT_INACTIVE, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_AccountLocked(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").
		WillReturnRows(masterUserRowUntil(string(hash), "Admin", "active", "DEV001", time.Now().Add(1*time.Hour)))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(int64(11), "Admin", true))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 ACCOUNT_LOCKED, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_BadPassword(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("OtherPass"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Admin", "active", "DEV001"))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(int64(11), "Admin", true))
	// recordFailedLogin (threshold 3, window 900s).
	f.tmock.ExpectExec(`UPDATE users SET failed_login_attempts = failed_login_attempts \+ 1`).
		WithArgs(3, int64(900), uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthLogin_SuccessAdmin(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Admin", "active", "DEV001"))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(int64(11), "Admin", true))
	// Admin → tanpa userVehicleIDs; sukses → reset counter last_login.
	f.tmock.ExpectExec(`UPDATE users SET last_login = NOW\(\),`).
		WithArgs(uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data models.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Token == "" || resp.Data.RefreshToken == "" || resp.Data.TokenType != "Bearer" {
		t.Fatalf("login response incomplete: %+v", resp.Data)
	}
	if resp.Data.User.Role != "Admin" || resp.Data.User.CompanyCode != "DEV001" {
		t.Fatalf("unexpected user payload: %+v", resp.Data.User)
	}
}

func TestAuthLogin_SuccessOperator(t *testing.T) {
	f := newAuthFixture(t)

	companyDBByCodeFn = func(string) (*sql.DB, error) { return f.db, nil }

	hash, _ := bcrypt.GenerateFromPassword([]byte("Secret@123"), bcrypt.MinCost)
	f.tmock.ExpectQuery(`FROM users WHERE email = \? AND deleted_at IS NULL`).
		WithArgs("u@dev001.io").WillReturnRows(masterUserRow(string(hash), "Operator", "active", "DEV001"))
	f.m.ExpectQuery(`SELECT id, role_override, is_active`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "role_override", "is_active"}).
			AddRow(int64(11), "", true))
	// Operator non-admin → vehicle scope di-load.
	f.m.ExpectQuery(`SELECT vehicle_id FROM user_vehicles`).WithArgs(uint64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"vehicle_id"}).AddRow(int64(42)))
	f.tmock.ExpectExec(`UPDATE users SET last_login = NOW\(\),`).
		WithArgs(uint64(7)).WillReturnResult(sqlmock.NewResult(0, 1))

	c, rec := loginCtx(t, loginBody)
	authLoginHandler(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data models.LoginResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.User.Role != "Operator" {
		t.Fatalf("unexpected role: %+v", resp.Data.User)
	}
}
