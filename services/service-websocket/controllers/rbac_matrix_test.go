package controllers

import (
	"net/http"
	"testing"

	"adatrack_gps/service-websocket/models"
)

// TestRowLevelVehicleAccess asserts the tm_user_vehicles filter (PRD §9.2):
// Admin sees all, Operator sees only assigned, unassigned → 403.
func TestRowLevelVehicleAccess(t *testing.T) {
	h := newHarness(t)

	t.Run("admin sees every vehicle of the tenant", func(t *testing.T) {
		access, _ := h.login(t, "admin@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if len(body["data"].([]any)) != 3 {
			t.Fatalf("admin vehicles = %d, want 3", len(body["data"].([]any)))
		}
	})

	t.Run("operator sees only assigned vehicles", func(t *testing.T) {
		access, _ := h.login(t, "operator@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		vehicles := body["data"].([]any)
		if len(vehicles) != 2 {
			t.Fatalf("operator vehicles = %d, want 2", len(vehicles))
		}
		for _, raw := range vehicles {
			id := int64(raw.(map[string]any)["id"].(float64))
			if id == 3 {
				t.Fatalf("operator received an unassigned vehicle %d", id)
			}
		}
	})

	t.Run("operator cannot read an unassigned vehicle", func(t *testing.T) {
		access, _ := h.login(t, "operator@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/3", access, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeUnauthorizedVehicle {
			t.Fatalf("error_code = %q, want %s", got, CodeUnauthorizedVehicle)
		}
	})

	t.Run("driver cannot read another driver's vehicle", func(t *testing.T) {
		access, _ := h.login(t, "driver@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1", access, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodeUnauthorizedVehicle {
			t.Fatalf("error_code = %q, want %s", got, CodeUnauthorizedVehicle)
		}
	})

	t.Run("driver can read the assigned vehicle", func(t *testing.T) {
		access, _ := h.login(t, "driver@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/3", access, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
		}
	})

	t.Run("history endpoint applies the same row-level rule", func(t *testing.T) {
		access, _ := h.login(t, "operator@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/3/history", access, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeUnauthorizedVehicle {
			t.Fatalf("error_code = %q, want %s", got, CodeUnauthorizedVehicle)
		}
	})
}

// TestTenantAccessRevokedIs401 asserts a deactivated membership row is rejected.
func TestTenantAccessRevokedIs401(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "operator@dev001.io", "Admin@123")

	h.store.mu.Lock()
	h.store.access["DEV001"][3] = accessRow{roleOverride: models.RoleOperator, isActive: false}
	h.store.mu.Unlock()

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %v)", resp.StatusCode, body)
	}
}

// TestRoleOverrideWins asserts tm_user_company_access.role_override replaces the
// global role inside the tenant (PRD §3.1).
func TestRoleOverrideWins(t *testing.T) {
	h := newHarness(t)
	// The driver is promoted to Manager inside DEV001: it must now see all rows.
	h.store.addAccess("DEV001", 4, models.RoleManager, true)

	access, _ := h.login(t, "driver@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if len(body["data"].([]any)) != 3 {
		t.Fatalf("manager-override vehicles = %d, want 3", len(body["data"].([]any)))
	}
}

// TestIncludeDeletedRequiresAdmin asserts the soft-delete read guard (§6.0.1).
func TestIncludeDeletedRequiresAdmin(t *testing.T) {
	h := newHarness(t)

	access, _ := h.login(t, "operator@dev001.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles?include_deleted=true", access, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %v)", resp.StatusCode, body)
	}

	// An Admin may, and the read is audited as SOFT_DELETED_VIEWED.
	deletedAt := "2026-09-01T00:00:00Z"
	h.store.addVehicle("DEV001", models.Vehicle{
		ID: 7, IMEI: "864201040512399", PlateNumber: "Z 9 ZZZ", Status: "inactive", DeletedAt: &deletedAt,
	})
	adminToken, _ := h.login(t, "admin@dev001.io", "Admin@123")
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles?include_deleted=true", adminToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	waitForAudit(t, h, func(rows []AuditRow) bool { return auditActionExists(rows, ActionSoftDeletedViewed) },
		"SOFT_DELETED_VIEWED audit row")
}
