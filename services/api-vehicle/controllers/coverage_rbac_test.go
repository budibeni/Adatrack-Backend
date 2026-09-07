package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ajb_gps/api-vehicle/models"
	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

func TestCanAccessVehicle(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(ctxAdminKey, true)
	c.Set(ctxAllowedKey, map[uint64]struct{}{1: {}})
	if !canAccessVehicle(c, 999) {
		t.Fatal("admin should access any vehicle")
	}

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Set(ctxAdminKey, false)
	c2.Set(ctxAllowedKey, map[uint64]struct{}{1: {}, 2: {}, 3: {}})
	c2.Request = httptest.NewRequest(http.MethodGet, "/vehicles/2", nil)
	c2.Request.RemoteAddr = "127.0.0.1:1234"
	if !canAccessVehicle(c2, 2) {
		t.Fatal("should access allowed vehicle")
	}
	c2.Request = httptest.NewRequest(http.MethodGet, "/vehicles/99", nil)
	c2.Request.RemoteAddr = "127.0.0.1:1234"
	if canAccessVehicle(c2, 99) {
		t.Fatal("should not access disallowed vehicle")
	}

	rec3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(rec3)
	c3.Set(ctxAdminKey, false)
	c3.Set(ctxAllowedKey, map[uint64]struct{}{})
	c3.Request = httptest.NewRequest(http.MethodGet, "/vehicles/1", nil)
	c3.Request.RemoteAddr = "127.0.0.1:1234"
	if canAccessVehicle(c3, 1) {
		t.Fatal("empty allowed list should deny all")
	}
}

func TestRequireVehicleAccess(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(ctxAdminKey, true)
	if !requireVehicleAccess(c, 1) {
		t.Fatal("admin should pass requireVehicleAccess")
	}

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Set(ctxAdminKey, false)
	c2.Set(ctxAllowedKey, map[uint64]struct{}{1: {}})
	c2.Request = httptest.NewRequest(http.MethodGet, "/vehicles/99", nil)
	if requireVehicleAccess(c2, 99) {
		t.Fatal("non-admin should be denied for disallowed vehicle")
	}
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec2.Code)
	}
}

func TestRequireAdminOrManager(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(ctxAdminKey, true)
	if !requireAdminOrManager(c) {
		t.Fatal("admin should pass")
	}

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Set(ctxAdminKey, false)
	c2.Set(ctxRoleKey, "Manager")
	if !requireAdminOrManager(c2) {
		t.Fatal("manager should pass")
	}

	rec3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(rec3)
	c3.Set(ctxAdminKey, false)
	c3.Set(ctxRoleKey, "Operator")
	c3.Request = httptest.NewRequest(http.MethodGet, "/routes", nil)
	c3.Request.RemoteAddr = "127.0.0.1:1234"
	if requireAdminOrManager(c3) {
		t.Fatal("operator should be denied")
	}
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec3.Code)
	}
}

func TestCompanyCodeOf(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(ctxCompanyCodeKey, "DEV001")
	if got := companyCodeOf(c); got != "DEV001" {
		t.Fatalf("companyCodeOf = %q, want DEV001", got)
	}

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	if got := companyCodeOf(c2); got != "" {
		t.Fatalf("companyCodeOf missing = %q, want empty", got)
	}
}

func TestLoadAuthUser(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	expected := models.AuthUser{ID: 1, CompanyCode: "DEV001", Role: "Admin"}
	c.Set(ctxUserKey, expected)
	if u, ok := loadAuthUser(c); !ok || u.ID != 1 {
		t.Fatalf("loadAuthUser = %+v, %v", u, ok)
	}

	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	if _, ok := loadAuthUser(c2); ok {
		t.Fatal("loadAuthUser should fail for missing user")
	}
}
