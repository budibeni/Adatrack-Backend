package controllers

import (
	"net/http"
	"testing"

	"adatrack_gps/service-websocket/models"
)

// TestCreateCompanyAutoProvision covers FR-5.5/PRD §4.2.1: platform-only
// auto-provisioning with an auto-created admin (`Admin@123`,
// must_change_password) and the audit triplet.
func TestCreateCompanyAutoProvision(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")

	resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, models.CreateCompanyRequest{
		Code: "newco", Name: "PT New Co", CountryCode: "ID", Timezone: "Asia/Jakarta",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", resp.StatusCode, body)
	}
	data := body["data"].(map[string]any)
	if data["code"] != "NEWCO" {
		t.Fatalf("code = %v, want NEWCO (normalised)", data["code"])
	}
	if data["database_name"] != "adatrack_gps_newco" {
		t.Fatalf("database_name = %v, want adatrack_gps_newco", data["database_name"])
	}
	admin := data["admin_user"].(map[string]any)
	if admin["email"] != "admin@newco.local" {
		t.Fatalf("admin email = %v, want admin@newco.local", admin["email"])
	}
	if admin["must_change_password"] != true {
		t.Fatalf("must_change_password = %v, want true", admin["must_change_password"])
	}

	// The tenant was provisioned with the requested country/timezone.
	if len(h.store.provisions) != 1 {
		t.Fatalf("provision calls = %d, want 1", len(h.store.provisions))
	}
	if h.store.provisions[0].CountryCode != "ID" || h.store.provisions[0].Timezone != "Asia/Jakarta" {
		t.Fatalf("provision options = %+v, want ID/Asia/Jakarta", h.store.provisions[0])
	}

	// The default password is bcrypt-hashed (never stored/logged in clear).
	adminRec, _ := h.store.UserByEmail(t.Context(), "admin@newco.local")
	if adminRec == nil {
		t.Fatalf("admin account was not created")
	}
	if adminRec.PasswordHash == h.service.settings.DefaultAdminPassword {
		t.Fatalf("admin password stored in clear text")
	}
	if adminRec.MustChangePassword != true || adminRec.GlobalRole != models.RoleAdmin {
		t.Fatalf("admin record = %+v, want Admin + must_change_password", adminRec)
	}

	// Audit triplet + row-level access row.
	waitForAudit(t, h, func(rows []AuditRow) bool {
		return auditActionExists(rows, ActionCompanyCreated) &&
			auditActionExists(rows, ActionTenantProvisioned) &&
			auditActionExists(rows, ActionAdminUserAutocreated)
	}, "FR-5.5 audit triplet")
	_, active, found, _ := h.store.TenantAccess(t.Context(), "NEWCO", adminRec.ID)
	if !found || !active {
		t.Fatalf("admin tenant access row missing")
	}
}

// TestCreateCompanyIsIdempotentWith409 covers FR-5.5 ("company yang sudah ada →
// tidak menimpa password admin (409 admin_exists)").
func TestCreateCompanyIsIdempotentWith409(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")

	resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, models.CreateCompanyRequest{
		Code: "DEV001", Name: "Development Company",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeCompanyExists {
		t.Fatalf("error_code = %q, want %s", got, CodeCompanyExists)
	}
	if len(h.store.provisions) != 0 {
		t.Fatalf("existing tenant was re-provisioned")
	}
	// The existing admin password must be untouched.
	admin, _ := h.store.UserByEmail(t.Context(), "admin@dev001.io")
	if admin == nil || admin.MustChangePassword {
		t.Fatalf("existing admin was modified: %+v", admin)
	}
}

// TestCreateCompanyValidation covers the FR-5.5/FR-5.6 guards.
func TestCreateCompanyValidation(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")

	t.Run("reserved DEFAULT code is rejected", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, models.CreateCompanyRequest{
			Code: "default", Name: "Sneaky Platform",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
		}
	})

	t.Run("missing name is a validation error", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, models.CreateCompanyRequest{
			Code: "ABC",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
		}
	})

	t.Run("unauthenticated request is 401", func(t *testing.T) {
		resp, _ := h.do(t, http.MethodPost, "/api/v1/companies", "", models.CreateCompanyRequest{
			Code: "ABC", Name: "ABC",
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})
}

// TestCreateCompanyProvisioningFailureIs503 asserts a failed provisioning is
// reported as 503 and is NOT audited as success.
func TestCreateCompanyProvisioningFailureIs503(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")
	h.store.mu.Lock()
	h.store.provisionErr = errProvisionFailed
	h.store.mu.Unlock()

	resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, models.CreateCompanyRequest{
		Code: "FAILCO", Name: "Fail Co",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
	if auditActionExists(h.store.auditsSnapshot(), ActionCompanyCreated) {
		t.Fatalf("a failed provisioning was audited as COMPANY_CREATED")
	}
}
