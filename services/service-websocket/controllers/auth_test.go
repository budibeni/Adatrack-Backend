package controllers

import (
	"net/http"
	"strings"
	"testing"

	"adatrack_gps/service-websocket/models"
)

// TestLoginSuccessIssuesTokensAndAudits verifies FR-5.7/PRD §9.1: a valid
// credential returns a token pair and records exactly one LOGIN_SUCCESS audit row.
func TestLoginSuccessIssuesTokensAndAudits(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if body["status"] != "success" {
		t.Fatalf("status field = %v, want success", body["status"])
	}
	data := body["data"].(map[string]any)
	if data["access_token"] == "" || data["refresh_token"] == "" {
		t.Fatalf("tokens missing: %v", data)
	}
	user := data["user"].(map[string]any)
	if user["company_code"] != "DEV001" || user["role"] != models.RoleAdmin {
		t.Fatalf("identity = %v, want DEV001/Admin", user)
	}
	// Password material must NEVER be part of the response (PRD §8.6).
	if strings.Contains(strings.ToLower(mustJSON(t, data)), "password_hash") {
		t.Fatalf("response leaks password material: %v", data)
	}

	if countAction(h.store.auditsSnapshot(), ActionLoginSuccess) != 1 {
		t.Fatalf("LOGIN_SUCCESS rows = %d, want 1", countAction(h.store.auditsSnapshot(), ActionLoginSuccess))
	}
}

// TestLoginInvalidPasswordAuditsFailure verifies the PRD §9.1 failure path.
func TestLoginInvalidPasswordAuditsFailure(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "wrong-password",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeInvalidCredentials {
		t.Fatalf("error_code = %q, want %s", got, CodeInvalidCredentials)
	}
	if countAction(h.store.auditsSnapshot(), ActionLoginFailure) != 1 {
		t.Fatalf("LOGIN_FAILURE audit missing")
	}
}

// TestLoginUnknownAccountIsIndistinguishable asserts the anti-enumeration
// behaviour: the same status/code as a wrong password.
func TestLoginUnknownAccountIsIndistinguishable(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "nobody@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeInvalidCredentials {
		t.Fatalf("error_code = %q, want %s", got, CodeInvalidCredentials)
	}
}

// TestLoginRateLimited enforces PRD §8.4 (5 attempts / 15 min).
func TestLoginRateLimited(t *testing.T) {
	h := newHarness(t)
	h.service.auth.cfg.LoginRateLimit = 2

	for i := 0; i < 2; i++ {
		resp, _ := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
			Email: "admin@dev001.io", Password: "wrong-password",
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, resp.StatusCode)
		}
	}
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeRateLimited {
		t.Fatalf("error_code = %q, want %s", got, CodeRateLimited)
	}
}

// TestLoginLockoutAfterRepeatedFailures verifies the account lockout rule.
func TestLoginLockoutAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	h.service.auth.cfg.LoginRateLimit = 100
	h.service.auth.cfg.LoginLockoutThreshold = 3

	for i := 0; i < 3; i++ {
		_, _ = h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
			Email: "admin@dev001.io", Password: "wrong-password",
		})
	}
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeAccountLocked {
		t.Fatalf("error_code = %q, want %s", got, CodeAccountLocked)
	}
}

// TestLoginValidation covers PRD §8.5 rule 1 (binding validation).
func TestLoginValidation(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "not-an-email", "password": "short",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeValidationError {
		t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
	}
	if _, ok := body["errors"].(map[string]any); !ok {
		t.Fatalf("validation response must list failing fields: %v", body)
	}
}

// TestTokenStoreUnavailableFailsClosed asserts a Redis outage yields 503 (never
// an unauthenticated/served request).
func TestTokenStoreUnavailableFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.kv.SetDown(true)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %v)", resp.StatusCode, body)
	}
}
