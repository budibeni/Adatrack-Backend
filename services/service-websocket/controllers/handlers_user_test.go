package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ajb_gps/service-websocket/models"

	"github.com/gin-gonic/gin"
)

// buildUserContext builds a POST /api/v1/users request context with the given
// caller identity + JSON body, returning the recorder for assertions.
func buildUserContext(body string, companyCode, role string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(ctxUserKey, models.AuthUser{ID: 2, CompanyCode: companyCode, Email: "platform@adatrackgps.local", Role: role})
	return c, rec
}

const validUserBody = `{"company_code":"ABLE01","email":"admin@able.co.id","password":"Secret@123","full_name":"Able Admin","role":"Admin"}`

// Governance: identitas tenant TIDAK boleh membuat user → 403 PLATFORM_ONLY
// sebelum sentuhan DB apa pun (appTenant sengaja nil untuk membuktikannya).
func TestUserCreate_TenantIdentityForbidden(t *testing.T) {
	appTenant = nil

	c, rec := buildUserContext(validUserBody, "DEF001", "Admin")
	userCreateHandler(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PLATFORM_ONLY") {
		t.Errorf("expected PLATFORM_ONLY, got %s", rec.Body.String())
	}
}

// Validasi murni berjalan SEBELUM infra: semua kasus ini harus 400 walau
// appTenant nil (membuktikan urutan gate → validasi → DB).
func TestUserCreate_ValidationErrors(t *testing.T) {
	appTenant = nil // tidak boleh di-dereference oleh jalur validasi

	cases := []struct {
		name string
		body string
		want string
	}{
		{"missing company", `{"email":"a@b.co","password":"Secret@123","full_name":"X"}`, "INVALID_REQUEST"},
		{"missing password", `{"company_code":"ABLE01","email":"a@b.co","full_name":"X"}`, "INVALID_REQUEST"},
		{"bad email", `{"company_code":"ABLE01","email":"bukan-email","password":"Secret@123","full_name":"X"}`, "INVALID_REQUEST"},
		{"weak password", `{"company_code":"ABLE01","email":"a@b.co","password":"pendek","full_name":"X"}`, "WEAK_PASSWORD"},
		{"invalid role", `{"company_code":"ABLE01","email":"a@b.co","password":"Secret@123","full_name":"X","role":"CEO"}`, "INVALID_REQUEST"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := buildUserContext(tc.body, "default", "SuperAdmin")
			userCreateHandler(c)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d (%s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("expected error_code %s, got %s", tc.want, rec.Body.String())
			}
		})
	}
}

// Role platform tidak boleh ditempel ke user tenant lewat API.
func TestUserCreate_PlatformRoleReserved(t *testing.T) {
	appTenant = nil

	body := `{"company_code":"ABLE01","email":"a@b.co","password":"Secret@123","full_name":"X","role":"SuperAdmin"}`
	c, rec := buildUserContext(body, "default", "SuperAdmin")
	userCreateHandler(c)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "PLATFORM_ROLE_RESERVED") {
		t.Errorf("expected PLATFORM_ROLE_RESERVED, got %s", rec.Body.String())
	}
}

// Konteks 'default' tidak boleh jadi rumah user tenant.
func TestUserCreate_PlatformCompanyRejected(t *testing.T) {
	appTenant = nil

	body := `{"company_code":"default","email":"a@b.co","password":"Secret@123","full_name":"X"}`
	c, rec := buildUserContext(body, "default", "SuperAdmin")
	userCreateHandler(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

// Identitas platform valid + body valid → lolos sampai infra gate (503 karena
// appTenant nil di unit test), membuktikan tidak ada dereferensi DB lebih awal.
func TestUserCreate_GatePassesReachesInfra(t *testing.T) {
	appTenant = nil

	c, rec := buildUserContext(validUserBody, "default", "SuperAdmin")
	userCreateHandler(c)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (tenant manager not initialized), got %d", rec.Code)
	}
}

// Allowlist endpoint platform.
func TestIsPlatformPath(t *testing.T) {
	for _, p := range []string{"/api/v1/companies", "/api/v1/users"} {
		if !isPlatformPath(p) {
			t.Errorf("%s should be a platform path", p)
		}
	}
	for _, p := range []string{"/api/v1/vehicles", "/api/v1/ws", "/api/v1/auth/login", ""} {
		if isPlatformPath(p) {
			t.Errorf("%s must NOT be a platform path", p)
		}
	}
}
