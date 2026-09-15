package controllers

import (
	"net/http"
	"testing"
)

// TestVehicleHistoryRangeValidation covers PRD §8.5 rules 2/4 on the history
// window (whitelist + boundary checks).
func TestVehicleHistoryRangeValidation(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	cases := []string{
		"?from=2026-09-15T10:00:00Z&to=2026-09-14T10:00:00Z", // from > to
		"?from=yesterday", // malformed timestamp
		"?from=2020-01-01T00:00:00Z&to=2026-09-15T00:00:00Z", // beyond HISTORY_MAX_RANGE_DAYS
	}
	for _, query := range cases {
		resp, body := h.do(t, http.MethodGet, "/api/v1/vehicles/1/history"+query, access, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %v)", query, resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("%s error_code = %q, want %s", query, got, CodeValidationError)
		}
	}
}

// TestInvalidVehicleIDParam asserts path-parameter validation (§8.5).
func TestInvalidVehicleIDParam(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	for _, path := range []string{"/api/v1/vehicles/abc", "/api/v1/vehicles/0", "/api/v1/vehicles/-3"} {
		resp, body := h.do(t, http.MethodGet, path, access, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 (body %v)", path, resp.StatusCode, body)
		}
		if got := errorCode(t, body); got != CodeValidationError {
			t.Fatalf("%s error_code = %q, want %s", path, got, CodeValidationError)
		}
	}
}

// TestUnknownRouteAndMethod asserts the PRD §8.1 error contract for unmatched
// routes and methods.
func TestUnknownRouteAndMethod(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	resp, body := h.do(t, http.MethodGet, "/api/v1/nope", access, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := errorCode(t, body); got != CodeRouteNotFound {
		t.Fatalf("error_code = %q, want %s", got, CodeRouteNotFound)
	}

	resp, body = h.do(t, http.MethodDelete, "/api/v1/vehicles", access, nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (body %v)", resp.StatusCode, body)
	}
	if got := errorCode(t, body); got != CodeMethodNotAllowed {
		t.Fatalf("error_code = %q, want %s", got, CodeMethodNotAllowed)
	}
}

// TestSecurityHeaders asserts the PRD §9.3 hardening headers.
func TestSecurityHeaders(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/livez", "", nil)
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options missing")
	}
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("X-Frame-Options missing")
	}
	if resp.Header.Get("Permissions-Policy") == "" {
		t.Fatalf("Permissions-Policy missing")
	}
	if resp.Header.Get("X-Request-ID") == "" {
		t.Fatalf("X-Request-ID missing (audit correlation, PRD §9.4)")
	}
}

// TestHealthzReportsDependencies asserts the readiness payload (PRD §10.2).
func TestHealthzReportsDependencies(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, "/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, body)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v, want ok", body["status"])
	}
	checks := body["checks"].(map[string]any)
	if checks["postgres_master"] != "ok" || checks["redis"] != "ok" {
		t.Fatalf("checks = %v, want postgres_master/redis ok", checks)
	}
}

// TestCORSAllowlist asserts only allowlisted origins are echoed (PRD §9.3).
func TestCORSAllowlist(t *testing.T) {
	h := newHarness(t)

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/livez", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow-origin = %q, want the allowlisted origin", got)
	}

	req.Header.Set("Origin", "http://evil.example.com")
	resp2, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if got := resp2.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("non-allowlisted origin was echoed: %q", got)
	}
}
