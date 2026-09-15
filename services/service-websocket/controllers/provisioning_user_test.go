package controllers

import (
	"net/http"
	"testing"

	"ajb_gps/service-websocket/models"
)

// TestCreateUserOnboarding covers FR-5.6: platform-only creation of a tenant
// account (master insert + tenant access + optional vehicle grants) + audit.
func TestCreateUserOnboarding(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")

	resp, body := h.do(t, http.MethodPost, "/api/v1/users", access, models.CreateUserRequest{
		Email:       "manager@dev001.io",
		FullName:    "Manager One",
		Role:        models.RoleManager,
		CompanyCode: "dev001",
		VehicleIDs:  []int64{1, 2},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %v)", resp.StatusCode, body)
	}
	data := body["data"].(map[string]any)
	if data["company_code"] != "DEV001" || data["role"] != models.RoleManager {
		t.Fatalf("identity = %v, want DEV001/Manager", data)
	}
	if data["must_change_password"] != true {
		t.Fatalf("must_change_password = %v, want true (default password)", data["must_change_password"])
	}
	if len(data["vehicle_ids"].([]any)) != 2 {
		t.Fatalf("vehicle grants = %v, want 2 ids", data["vehicle_ids"])
	}

	// The account can log in with the default password and sees its grants.
	newAccess, _ := h.login(t, "manager@dev001.io", "Admin@123")
	claims, err := h.service.auth.ParseAccessToken(newAccess)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.Role != models.RoleManager || claims.CompanyCode != "DEV001" {
		t.Fatalf("claims = %+v, want Manager/DEV001", claims)
	}

	waitForAudit(t, h, func(rows []AuditRow) bool { return auditActionExists(rows, ActionUserCreated) },
		"USER_CREATED audit row")
}

// TestCreateUserGuards covers the FR-5.6 guard set.
func TestCreateUserGuards(t *testing.T) {
	h := newHarness(t)
	platform, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")

	t.Run("SuperAdmin role is reserved (403 PLATFORM_ROLE_RESERVED)", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "sneaky@dev001.io", FullName: "Sneaky", Role: models.RoleSuperAdmin, CompanyCode: "DEV001",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodePlatformRoleReserved {
			t.Fatalf("error_code = %q, want %s", got, CodePlatformRoleReserved)
		}
	})

	t.Run("DEFAULT as company_code is rejected", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "someone@default.local", FullName: "Someone", Role: models.RoleAdmin,
			CompanyCode: models.PlatformCompanyCode,
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
		}
	})

	t.Run("unknown company is 404", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "someone@ghost.io", FullName: "Someone", Role: models.RoleAdmin, CompanyCode: "GHOST",
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeCompanyNotFound {
			t.Fatalf("error_code = %q, want %s", got, CodeCompanyNotFound)
		}
	})

	t.Run("duplicate email is 409", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "admin@dev001.io", FullName: "Duplicate", Role: models.RoleAdmin, CompanyCode: "DEV001",
		})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeUserExists {
			t.Fatalf("error_code = %q, want %s", got, CodeUserExists)
		}
	})

	t.Run("foreign vehicle_ids are rejected", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "grant@dev001.io", FullName: "Grant", Role: models.RoleOperator,
			CompanyCode: "DEV001", VehicleIDs: []int64{1, 4242},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
		}
	})

	t.Run("tenant token cannot create users", func(t *testing.T) {
		tenant, _ := h.login(t, "admin@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", tenant, models.CreateUserRequest{
			Email: "x@dev001.io", FullName: "X", Role: models.RoleAdmin, CompanyCode: "DEV001",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodePlatformOnly {
			t.Fatalf("error_code = %q, want %s", got, CodePlatformOnly)
		}
	})

	t.Run("invalid role enum is a validation error", func(t *testing.T) {
		resp, body := h.do(t, http.MethodPost, "/api/v1/users", platform, models.CreateUserRequest{
			Email: "enum@dev001.io", FullName: "Enum", Role: "Wizard", CompanyCode: "DEV001",
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
		}
	})
}
