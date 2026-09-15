package controllers

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"ajb_gps/service-websocket/models"
)

// TestAPIRateLimitPerUser enforces PRD §8.4 (100 requests / minute / user).
func TestAPIRateLimitPerUser(t *testing.T) {
	h := newHarness(t)
	h.service.settings.APIRateLimit = 3
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	for i := 0; i < 3; i++ {
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200 (body %v)", i, resp.StatusCode, body)
		}
	}
	resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeRateLimited {
		t.Fatalf("error_code = %q, want %s", got, CodeRateLimited)
	}

	// A different user has its own budget (per-user keying).
	other, _ := h.login(t, "operator@dev001.io", "Admin@123")
	if resp, _ := h.do(t, http.MethodGet, "/api/v1/vehicles", other, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("second user status = %d, want 200", resp.StatusCode)
	}
}

// TestRequestIDPropagation asserts the correlation id is echoed and reused
// (PRD §9.4 correlation with the audit row).
func TestRequestIDPropagation(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/livez", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Request-ID", "trace-abc-123")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("X-Request-ID"); got != "trace-abc-123" {
		t.Fatalf("X-Request-ID = %q, want the caller's id", got)
	}

	// Without a header the server generates one.
	resp2, err := h.server.Client().Get(h.server.URL + "/livez")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.Header.Get("X-Request-ID") == "" {
		t.Fatalf("server did not generate a request id")
	}
}

// TestMalformedJSONBody asserts a broken body yields VALIDATION_ERROR (§8.5).
func TestMalformedJSONBody(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/auth/login",
		bytes.NewBufferString("{not json"))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestBodyLimit asserts the request body cap is enforced (PRD §8.5 rule 4).
func TestBodyLimit(t *testing.T) {
	h := newHarness(t)
	h.service.settings.MaxBodyBytes = 32

	payload := strings.Repeat("a", 256)
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"email": "admin@dev001.io", "password": payload,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeValidationError {
		t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
	}
}

// TestRefreshValidation asserts the refresh endpoint validates its body.
func TestRefreshValidation(t *testing.T) {
	h := newHarness(t)

	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/refresh", "", map[string]string{"refresh_token": "short"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeValidationError {
		t.Fatalf("error_code = %q, want %s", got, CodeValidationError)
	}
}

// TestLogoutWithoutBodyIsAllowed asserts an empty body still revokes the token.
func TestLogoutWithoutBodyIsAllowed(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/auth/logout", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	_, body := h.do(t, http.MethodGet, "/api/v1/vehicles", access, nil)
	if got := errorCode(t, body); got != CodeTokenRevoked {
		t.Fatalf("error_code = %q, want %s", got, CodeTokenRevoked)
	}
}

// TestLoginResetsFailureCounter asserts a successful login clears the lockout
// counters (PRD §9.1).
func TestLoginResetsFailureCounter(t *testing.T) {
	h := newHarness(t)
	h.service.auth.cfg.LoginRateLimit = 100

	_, _ = h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "wrong-password",
	})
	_ = h.do
	if resp, _ := h.do(t, http.MethodPost, "/api/v1/auth/login", "", models.LoginRequest{
		Email: "admin@dev001.io", Password: "Admin@123",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("valid login after a failure status = %d, want 200", resp.StatusCode)
	}

	rec, _ := h.store.UserByEmail(t.Context(), "admin@dev001.io")
	if rec.FailedAttempts != 0 || rec.LockedUntil != nil {
		t.Fatalf("failure counter not reset: %+v", rec)
	}
}
