package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// buildRBACContext constructs a gin.Context carrying RBAC context values.
func buildRBACContext(admin bool, allowed map[uint64]struct{}) *gin.Context {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set(ctxAdminKey, admin)
	c.Set(ctxAllowedKey, allowed)
	return c
}

// Non-admin hanya bisa mengakses vehicle dalam user_vehicles (row-level).
func TestCanAccessVehicle_NonAdmin(t *testing.T) {
	c := buildRBACContext(false, map[uint64]struct{}{1: {}, 2: {}})

	if !canAccessVehicle(c, 1) {
		t.Error("expected access to vehicle 1")
	}
	if !canAccessVehicle(c, 2) {
		t.Error("expected access to vehicle 2")
	}
	if canAccessVehicle(c, 999) {
		t.Error("expected denial for unassigned vehicle 999")
	}
}

// ADMIN melihat semua vehicle (FR-5.1 / PRD §3.1).
func TestCanAccessVehicle_Admin(t *testing.T) {
	c := buildRBACContext(true, nil)
	if !canAccessVehicle(c, 1) || !canAccessVehicle(c, 424242) {
		t.Error("admin should access every vehicle")
	}
	if !isAdmin(c) {
		t.Error("expected isAdmin true")
	}
}

// User tanpa vehicle apa pun tidak boleh mengakses kendaraan orang lain.
func TestCanAccessVehicle_EmptyAllowedSet(t *testing.T) {
	c := buildRBACContext(false, map[uint64]struct{}{})
	if canAccessVehicle(c, 1) {
		t.Error("expected denial with empty allowed set")
	}
}

// requireVehicleAccess menulis 403 saat akses ditolak.
func TestRequireVehicleAccessDenied(t *testing.T) {
	c := buildRBACContext(false, map[uint64]struct{}{1: {}})
	if ok := requireVehicleAccess(c, 123); ok {
		t.Fatal("expected false from requireVehicleAccess")
	}
	if c.Writer.Status() != 403 {
		t.Errorf("expected 403, got %d", c.Writer.Status())
	}
}
