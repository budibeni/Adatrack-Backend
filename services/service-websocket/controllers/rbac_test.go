package controllers

import (
	"net/http"
	"testing"

	"adatrack_gps/service-websocket/models"
)

// TestRequireAuthMatrix covers the PRD §3.1 acceptance "401/403 benar
// (tanpa token, cross-tenant, tanpa hak vehicle)".
func TestRequireAuthMatrix(t *testing.T) {
	h := newHarness(t)

	t.Run("no token is 401 UNAUTHORIZED", func(t *testing.T) {
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodeUnauthorized {
			t.Fatalf("error_code = %q, want %s", got, CodeUnauthorized)
		}
	})

	t.Run("garbage token is 401 TOKEN_INVALID", func(t *testing.T) {
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", "not-a-jwt", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodeTokenInvalid {
			t.Fatalf("error_code = %q, want %s", got, CodeTokenInvalid)
		}
	})

	t.Run("deactivated account is 401 ACCOUNT_INACTIVE", func(t *testing.T) {
		access, _ := h.login(t, "driver@dev001.io", "Admin@123")
		h.store.mu.Lock()
		h.store.users["driver@dev001.io"].IsActive = false
		h.store.mu.Unlock()

		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodeAccountInactive {
			t.Fatalf("error_code = %q, want %s", got, CodeAccountInactive)
		}
	})

	t.Run("platform token on a tenant route is 403 PLATFORM_SCOPE", func(t *testing.T) {
		access, _ := h.login(t, "platform@adatrackgps.local", "Platform@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodePlatformScope {
			t.Fatalf("error_code = %q, want %s", got, CodePlatformScope)
		}
	})

	t.Run("tenant token on a platform route is 403 PLATFORM_ONLY", func(t *testing.T) {
		access, _ := h.login(t, "admin@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodPost, "/api/v1/companies", access, map[string]any{
			"code": "NEWCO", "name": "New Co",
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodePlatformOnly {
			t.Fatalf("error_code = %q, want %s", got, CodePlatformOnly)
		}
	})

	t.Run("must_change_password blocks business endpoints", func(t *testing.T) {
		access, _ := h.login(t, "newadmin@dev001.io", "Admin@123")
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		if got := errorCode(t, body); got != CodePasswordChangeNeeded {
			t.Fatalf("error_code = %q, want %s", got, CodePasswordChangeNeeded)
		}
		// ...but logout stays available (PRD §4.2.1).
		if resp, _ := h.do(t, http.MethodPost, "/api/v1/auth/logout", access, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("logout status = %d, want 200", resp.StatusCode)
		}
	})
}

// TestCrossTenantIsolation asserts a user only ever sees their own tenant rows
// (PRD §3.1: cross-tenant → 403; the tenant comes from the token).
func TestCrossTenantIsolation(t *testing.T) {
	h := newHarness(t)
	// A second tenant with its own vehicle 42 and admin.
	h.store.addUser(20, "OTHER1", "admin@other1.io", models.RoleAdmin, "Admin@123", false)
	h.store.addAccess("OTHER1", 20, models.RoleAdmin, true)
	h.store.addVehicle("OTHER1", models.Vehicle{ID: 42, IMEI: "999999999999999", PlateNumber: "X 1 XXX", Status: "active"})
	h.store.companies["OTHER1"] = true

	access, _ := h.login(t, "admin@other1.io", "Admin@123")
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	vehicles := body["data"].([]any)
	if len(vehicles) != 1 {
		t.Fatalf("vehicles = %d, want 1 (cross-tenant leak?)", len(vehicles))
	}
	first := vehicles[0].(map[string]any)
	if int64(first["id"].(float64)) != 42 {
		t.Fatalf("vehicle = %v, want id 42 (own tenant only)", first)
	}

	// The DEV001 vehicle ids must NOT be reachable from the OTHER1 token.
	resp, body = h.do(t, http.MethodGet, "/api/v1/vehicles/1", access, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeVehicleNotFound {
		t.Fatalf("error_code = %q, want %s", got, CodeVehicleNotFound)
	}
}

// TestUnknownVehicleIs404 asserts 404 vs 403 discrimination (IDOR hygiene).
func TestUnknownVehicleIs404(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/9999", access, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeVehicleNotFound {
		t.Fatalf("error_code = %q, want %s", got, CodeVehicleNotFound)
	}
}
