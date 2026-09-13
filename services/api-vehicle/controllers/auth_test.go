package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func testClaims() *tokenClaims {
	return &tokenClaims{
		UserID:      1,
		CompanyCode: "DEV001",
		CompanyID:   1,
		Email:       "admin@dev001.io",
		Role:        "Admin",
	}
}

func TestParseTokenRejectsMissingCompany(t *testing.T) {
	// Token tanpa company_code harus ditolak (multi-tenant wajib).
	origSecret := appCfg
	defer func() { appCfg = origSecret }()
	cfg := newTestCfg()
	appCfg = cfg

	claims := &tokenClaims{UserID: 9, Email: "x@y.z", Role: "Operator"}
	claims.ExpiresAt = jwtNewExp(time.Hour)
	tok := jwtSignForTest(claims, []byte(cfg.JWT.Secret))
	if _, err := parseToken(tok); err == nil {
		t.Fatal("token without company_code must be rejected")
	}
}

func TestExtractTokenBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Authorization", "Bearer abc123")
	if got := extractToken(c); got != "abc123" {
		t.Errorf("extractToken = %q", got)
	}
}

func TestFailureRateLimiter(t *testing.T) {
	l := newFailureRateLimiter(5, time.Minute)
	if !l.allow("1.1.1.1") {
		t.Fatal("expected allow initially")
	}
	for i := 0; i < 5; i++ {
		l.recordFailure("1.1.1.1")
	}
	if l.allow("1.1.1.1") {
		t.Fatal("expected block after 5 failures")
	}
	l.reset("1.1.1.1")
	if !l.allow("1.1.1.1") {
		t.Fatal("expected allow after reset")
	}
}

func TestEffectiveRole(t *testing.T) {
	if got := effectiveRole("Admin", ""); got != "Admin" {
		t.Fatalf("effectiveRole override empty = %q", got)
	}
	if got := effectiveRole("Driver", "Manager"); got != "Manager" {
		t.Fatalf("effectiveRole override = %q", got)
	}
}

func TestIsAdminRole(t *testing.T) {
	if !isAdminRole("Admin") || !isAdminRole("admin") {
		t.Fatal("admin role check failed")
	}
	if isAdminRole("Operator") {
		t.Fatal("operator must not be admin")
	}
}

func TestFmtParseUint(t *testing.T) {
	if n, err := fmtParseUint("42"); err != nil || n != 42 {
		t.Fatalf("fmtParseUint(42) = %d, %v", n, err)
	}
	if _, err := fmtParseUint("12x"); err == nil {
		t.Fatal("non-numeric must error")
	}
}

func TestAtoiDefault(t *testing.T) {
	if got := atoiDefault("", 100); got != 100 {
		t.Fatalf("atoiDefault empty = %d", got)
	}
	if got := atoiDefault("7", 100); got != 7 {
		t.Fatalf("atoiDefault 7 = %d", got)
	}
	if got := atoiDefault("abc", 100); got != 100 {
		t.Fatalf("atoiDefault abc = %d", got)
	}
}
