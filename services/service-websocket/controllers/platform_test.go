package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// buildPlatformContext constructs a gin.Context carrying an authenticated
// identity (AuthUser) like installAuthContext would, plus the recorder so
// tests can assert on the response body.
func buildPlatformContext(companyCode, role string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/companies", strings.NewReader(`{"code":"ABLE01","name":"PT Able"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(ctxUserKey, models.AuthUser{ID: 2, CompanyCode: companyCode, Email: "u@x.io", Role: role})
	return c, rec
}

// Hanya SuperAdmin pada konteks platform ('default') yang dianggap platform admin.
func TestIsPlatformAdmin(t *testing.T) {
	if c, _ := buildPlatformContext("default", "SuperAdmin"); !isPlatformAdmin(c) {
		t.Error("default+SuperAdmin must be platform admin")
	}
	if c, _ := buildPlatformContext("DEFAULT", "superadmin"); !isPlatformAdmin(c) {
		t.Error("matching must be case-insensitive")
	}
	if c, _ := buildPlatformContext("DEF001", "Admin"); isPlatformAdmin(c) {
		t.Error("tenant Admin must NOT be platform admin")
	}
	if c, _ := buildPlatformContext("DEF001", "Operator"); isPlatformAdmin(c) {
		t.Error("tenant Operator must NOT be platform admin")
	}
	if c, _ := buildPlatformContext("default", "Admin"); isPlatformAdmin(c) {
		t.Error("default context without SuperAdmin role must NOT be platform admin")
	}
}

// Governance inti: Admin tenant TIDAK boleh mendaftarkan company baru
// → 403 PLATFORM_ONLY (dicek SEBELUM sentuhan ke tenant manager apa pun).
func TestCompanyCreate_TenantAdminForbidden(t *testing.T) {
	appTenant = nil // memastikan penolakan terjadi di gate RBAC, bukan karena infra

	c, rec := buildPlatformContext("DEF001", "Admin")
	companyCreateHandler(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PLATFORM_ONLY") {
		t.Errorf("expected error_code PLATFORM_ONLY, got %s", rec.Body.String())
	}
}

// Gate lolos utk SuperAdmin platform → lanjut sampai cek tenant manager
// (appTenant nil di unit test → 503), membuktikan tidak ada dereferensi DB.
func TestCompanyCreate_PlatformAdminPassesGate(t *testing.T) {
	appTenant = nil // tanpa infra: kalau gate benar, gagal di cek appTenant (503)

	c, rec := buildPlatformContext("default", "SuperAdmin")
	companyCreateHandler(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (tenant manager not initialized), got %d", rec.Code)
	}
}

// Non-SuperAdmin pada konteks default juga ditolak di gate (defense-in-depth).
func TestCompanyCreate_DefaultContextNonSuperAdmin(t *testing.T) {
	appTenant = nil

	c, rec := buildPlatformContext("default", "Operator")
	companyCreateHandler(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}
