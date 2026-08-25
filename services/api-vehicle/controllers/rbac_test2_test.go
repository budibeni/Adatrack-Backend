package controllers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func init() {
	gin.SetMode(gin.TestMode)
	// Metrik ter-init HANYA lewat RegisterMetrics (internal/metrics.go), bukan
	// package init. Panggil sekali per biner test agar recordRBACDenial dan
	// RBACDenialsTotal.WithLabelValues aman dari nil-pointer.
	_ = internal.RegisterMetrics(prometheus.NewRegistry())
}

func TestBaseHelpers(t *testing.T) {
	if got := placeholders(3); got != "?,?,?" {
		t.Fatalf("placeholders(3) = %q", got)
	}
	if got := placeholders(0); got != "" {
		t.Fatalf("placeholders(0) = %q", got)
	}

	// mapKeys has stable (sorted) order
	got := mapKeys(map[uint64]struct{}{3: {}, 1: {}, 2: {}})
	if len(got) != 3 || got[0] != uint64(1) || got[1] != uint64(2) || got[2] != uint64(3) {
		t.Fatalf("mapKeys order = %v", got)
	}

	if nullableStr("") != nil || nullableStr("x") != "x" {
		t.Fatal("nullableStr wrong")
	}
	sp := "ptr"
	if nullableStrPtr(nil) != nil || nullableStrPtr(&sp) != "ptr" {
		t.Fatal("nullableStrPtr wrong")
	}
	if nullableUint(nil) != nil || nullableUint(&zeroU64) != nil {
		t.Fatal("nullableUint nil/zero wrong")
	}
	u := uint64(7)
	if nullableUint(&u) != uint64(7) {
		t.Fatal("nullableUint valid wrong")
	}

	now := nowForTest()
	if nullableTimeP(sql.NullTime{}) != nil {
		t.Fatal("nullableTimeP NULL wrong")
	}
	if got := nullableTimeP(sql.NullTime{Time: now, Valid: true}); got == nil {
		t.Fatal("nullableTimeP valid wrong")
	}
	if nullableFloat(sql.NullFloat64{}) != nil {
		t.Fatal("nullableFloat NULL wrong")
	}
	if nullableFloat(sql.NullFloat64{Float64: 1.5, Valid: true}) == nil {
		t.Fatal("nullableFloat valid wrong")
	}
	if nullableStrP(sql.NullString{}) != nil {
		t.Fatal("nullableStrP NULL wrong")
	}
	if nullableStrP(sql.NullString{String: "s", Valid: true}) == nil {
		t.Fatal("nullableStrP valid wrong")
	}
	if nullableUint64(sql.NullInt64{}) != nil {
		t.Fatal("nullableUint64 NULL wrong")
	}
	if nullableUint64(sql.NullInt64{Int64: 9, Valid: true}) == nil {
		t.Fatal("nullableUint64 valid wrong")
	}
}

func TestJoinChecks(t *testing.T) {
	if joinChecks(nil) != "" {
		t.Fatal("empty join wrong")
	}
	if got := joinChecks([]string{"mysql:ok", "redis:ok"}); got != "mysql:ok,redis:ok" {
		t.Fatalf("joinChecks = %q", got)
	}
}

func TestPaginationParams(t *testing.T) {
	run := func(q string) (int, int) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+q, nil)
		return paginationParams(c)
	}
	if p, l := run(""); p != 1 || l != 100 {
		t.Fatalf("empty => %d,%d", p, l)
	}
	if p, l := run("page=3&limit=50"); p != 3 || l != 50 {
		t.Fatalf("valid => %d,%d", p, l)
	}
	if p, l := run("page=0&limit=-1"); p != 1 || l != 100 {
		t.Fatalf("invalid => %d,%d", p, l)
	}
	if p, l := run("limit=999"); p != 1 || l != 100 {
		t.Fatalf("over-max => %d,%d", p, l)
	}
	if p, l := run("page=abc&limit=xyz"); p != 1 || l != 100 {
		t.Fatalf("non-numeric => %d,%d", p, l)
	}
}

func TestAtoiDefaultExtra(t *testing.T) {
	if atoiDefault("", 5) != 5 || atoiDefault("7", 5) != 7 || atoiDefault("ab", 9) != 9 || atoiDefault("0", 9) != 0 {
		t.Fatal("atoiDefault wrong")
	}
}

func TestRBACPureHelpers(t *testing.T) {
	if !isAdminRole("Admin") || !isAdminRole(" admin ") || isAdminRole("operator") {
		t.Fatal("isAdminRole wrong")
	}
	if !isManagerRole("manager") || !isManagerRole("adatrack_MANAGER") || isManagerRole("admin") {
		t.Fatal("isManagerRole wrong")
	}
	if effectiveRole("Operator", "") != "Operator" {
		t.Fatal("effectiveRole default wrong")
	}
	if effectiveRole("Operator", "Admin") != "Admin" {
		t.Fatal("effectiveRole override wrong")
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ctxUserKey, models.AuthUser{ID: 1, CompanyCode: "DEV001", Role: "Admin"})
	c.Set(ctxAdminKey, true)
	c.Set(ctxCompanyCodeKey, "DEV001")
	c.Set(ctxAllowedKey, map[uint64]struct{}{5: {}})
	c.Set(ctxRoleKey, "Admin")

	if u, ok := loadAuthUser(c); !ok || u.ID != 1 {
		t.Fatal("loadAuthUser wrong")
	}
	if !isAdmin(c) {
		t.Fatal("isAdmin should be true")
	}
	if companyCodeOf(c) != "DEV001" {
		t.Fatal("companyCodeOf wrong")
	}
	if got := accessibleVehicleIDs(c); len(got) != 1 {
		t.Fatal("accessibleVehicleIDs wrong")
	}
	if !canAccessVehicle(c, 5) {
		t.Fatal("admin should access any vehicle")
	}

	// non-admin denied via requireRole
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/", nil)
	c2.Set(ctxRoleKey, "operator")
	if requireRole(c2, "Admin", "Manager") {
		t.Fatal("operator should not pass requireRole(admin,manager)")
	}
	if c2.Writer.Status() != http.StatusForbidden {
		t.Fatalf("status = %d want 403", c2.Writer.Status())
	}
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/", nil)
	c3.Set(ctxRoleKey, "Manager")
	if !requireRole(c3, "Manager", "Admin") {
		t.Fatal("manager should pass requireRole")
	}

	// requireAdminOrManager
	c4, _ := gin.CreateTestContext(httptest.NewRecorder())
	c4.Request = httptest.NewRequest("GET", "/", nil)
	c4.Set(ctxAdminKey, true)
	if !requireAdminOrManager(c4) {
		t.Fatal("admin should pass requireAdminOrManager")
	}
	c5, _ := gin.CreateTestContext(httptest.NewRecorder())
	c5.Request = httptest.NewRequest("GET", "/", nil)
	c5.Set(ctxRoleKey, "operator")
	if requireAdminOrManager(c5) {
		t.Fatal("operator should fail requireAdminOrManager")
	}
}