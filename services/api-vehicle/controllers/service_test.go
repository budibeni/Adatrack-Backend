package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// testSettings is the minimal valid configuration used for wiring assertions.
func testSettings() Settings {
	return Settings{
		HTTPAddr:        ":0",
		JWTSecret:       strings.Repeat("t", 40),
		JWTIssuer:       "test",
		DefaultPageSize: 100,
		MaxPageSize:     1000,
	}
}

// TestServiceHandlerAndStoreWiring: the engine is always built, and the store is
// only reported as a *PostgresStore when it really is one (main.go uses this to
// detect a misconfigured deployment).
func TestServiceHandlerAndStoreWiring(t *testing.T) {
	svc := NewService(Deps{Settings: testSettings(), Store: newFakeStore()})
	if svc.Handler() == nil {
		t.Fatal("Handler() must return the built engine")
	}
	if pg, ok := svc.PostgresStore(); ok || pg != nil {
		t.Errorf("the fake store must not be reported as a PostgresStore: %v", pg)
	}

	pgSvc := NewService(Deps{Settings: testSettings(), Store: NewPostgresStore(nil)})
	pg, ok := pgSvc.PostgresStore()
	if !ok || pg == nil {
		t.Fatalf("PostgresStore() = (%v,%v), want the production store", pg, ok)
	}
}

// TestServiceLiveStateFallback: the overlay source falls back to the KV adapter
// unless an explicit reader is injected (phase B6 wiring).
func TestServiceLiveStateFallback(t *testing.T) {
	kv, _ := newMiniredisKV(t)

	fromKV := NewService(Deps{Settings: testSettings(), Store: newFakeStore(), KV: kv})
	if fromKV.live != LiveStateReader(kv) {
		t.Error("without an explicit reader the KV adapter must back the live overlay")
	}

	stub := newStubLive()
	explicit := NewService(Deps{Settings: testSettings(), Store: newFakeStore(), KV: kv, Live: stub})
	if explicit.live != LiveStateReader(stub) {
		t.Error("an injected reader must win over the KV fallback")
	}

	none := NewService(Deps{Settings: testSettings(), Store: newFakeStore()})
	if none.live != nil {
		t.Error("without KV and without a reader the overlay must stay disabled")
	}
}

// TestVehicleStoreErr hides every persistence detail behind a generic 503.
func TestVehicleStoreErr(t *testing.T) {
	secret := errors.New("pq: relation \"tm_vehicles\" does not exist for user adatrack")
	err := vehicleStoreErr(secret)

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error = %T, want *APIError", err)
	}
	if apiErr.Status != http.StatusServiceUnavailable || apiErr.Code != CodeServiceUnavailable {
		t.Errorf("got %d %s, want 503 %s", apiErr.Status, apiErr.Code, CodeServiceUnavailable)
	}
	if strings.Contains(apiErr.Message, "tm_vehicles") {
		t.Errorf("internals leaked to the client: %q", apiErr.Message)
	}
}

// TestDeniedListGuard: only the tenant Admin may look behind the soft-delete
// veil (PRD §6.0.1).
func TestDeniedListGuard(t *testing.T) {
	operator := operatorIdentity(1)
	admin := adminIdentity()
	manager := &tenantIdentity{userID: 3, role: models.RoleManager, companyCode: "DEV001", allVehicles: true}

	if err := deniedListGuard(operator, true); err == nil || err.Status != http.StatusForbidden {
		t.Errorf("operator + include_deleted = %v, want 403", err)
	}
	if err := deniedListGuard(manager, true); err == nil || err.Status != http.StatusForbidden {
		t.Errorf("manager + include_deleted = %v, want 403", err)
	}
	if err := deniedListGuard(admin, true); err != nil {
		t.Errorf("admin + include_deleted = %v, want nil", err)
	}
	if err := deniedListGuard(operator, false); err != nil {
		t.Errorf("operator without include_deleted = %v, want nil", err)
	}
}

// TestErrorConstructors pins the status + error_code of every helper, so the
// PRD §8.1 catalogue cannot drift per endpoint.
func TestErrorConstructors(t *testing.T) {
	cases := []struct {
		name   string
		err    *APIError
		status int
		code   string
	}{
		{"unauthorized", errUnauthorized("nope"), http.StatusUnauthorized, CodeUnauthorized},
		{"forbidden", errForbidden(CodeForbidden, "nope"), http.StatusForbidden, CodeForbidden},
		{"not found", errNotFound(CodeVehicleNotFound, "gone"), http.StatusNotFound, CodeVehicleNotFound},
		{"conflict", errConflict("duplicate"), http.StatusConflict, CodeConflict},
		{"bad request", errBadRequest(CodeInvalidTransition, "nope"), http.StatusBadRequest, CodeInvalidTransition},
		{"rate limited", errRateLimited("slow down"), http.StatusTooManyRequests, CodeRateLimited},
		{"internal", errInternal("boom"), http.StatusInternalServerError, CodeInternalError},
		{"unavailable", errUnavailable("down"), http.StatusServiceUnavailable, CodeServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Status != tc.status || tc.err.Code != tc.code {
				t.Errorf("got %d %s, want %d %s", tc.err.Status, tc.err.Code, tc.status, tc.code)
			}
			if want := tc.code + ": " + tc.err.Message; tc.err.Error() != want {
				t.Errorf("Error() = %q, want %q", tc.err.Error(), want)
			}
		})
	}

	if err := errValidation("bad input", map[string]string{"imei": "is required"}); err.Status != http.StatusBadRequest ||
		err.Code != CodeValidationError || err.Fields["imei"] != "is required" {
		t.Errorf("errValidation = %+v, want 400 VALIDATION_ERROR with the field map", err)
	}
	if err := NewAPIError(http.StatusTeapot, "TEAPOT", "short and stout"); err.Status != http.StatusTeapot ||
		err.Code != "TEAPOT" || err.Message != "short and stout" {
		t.Errorf("NewAPIError = %+v", err)
	}
}

// TestAsAPIError: unknown failures are normalised to a generic 500 so internals
// never reach the client.
func TestAsAPIError(t *testing.T) {
	if err := asAPIError(nil); err.Status != http.StatusInternalServerError || err.Code != CodeInternalError {
		t.Errorf("asAPIError(nil) = %+v, want 500 INTERNAL_ERROR", err)
	}
	original := errConflict("dup")
	if err := asAPIError(original); err != original {
		t.Error("an *APIError must pass through untouched")
	}
	err := asAPIError(fmt.Errorf("pgx: connection refused to 10.0.0.5"))
	if err.Status != http.StatusInternalServerError || strings.Contains(err.Message, "10.0.0.5") {
		t.Errorf("asAPIError = %+v, want a sanitised 500", err)
	}
}

// TestRespondErrorWritesEnvelope asserts the single error writer emits the
// PRD §8.1 envelope (status, code, message, RFC3339 timestamp, fields).
func TestRespondErrorWritesEnvelope(t *testing.T) {
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles/x", "", nil)
	respondError(c, errValidation("request validation failed", map[string]string{"limit": "invalid"}))

	var env models.ErrorEnvelope
	if err := parseJSON(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if rec.Code != http.StatusBadRequest || env.Status != "error" || env.ErrorCode != CodeValidationError {
		t.Fatalf("got %d %+v, want 400 VALIDATION_ERROR", rec.Code, env)
	}
	if env.Errors["limit"] != "invalid" {
		t.Errorf("field errors = %v, want limit=invalid", env.Errors)
	}
	if _, err := time.Parse(time.RFC3339, env.Timestamp); err != nil {
		t.Errorf("timestamp %q is not RFC3339: %v", env.Timestamp, err)
	}
}

// TestRespondOKAndCreated covers the success envelopes (with pagination and
// without).
func TestRespondOKAndCreated(t *testing.T) {
	c, rec := testContext(http.MethodGet, "/api/v1/vehicles", "", nil)
	respondOK(c, []models.Vehicle{{ID: 1, IMEI: "864"}}, pagination(2, 50, 120))

	var page struct {
		Status     string             `json:"status"`
		Data       []models.Vehicle   `json:"data"`
		Pagination *models.Pagination `json:"pagination"`
	}
	if err := parseJSON(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if rec.Code != http.StatusOK || page.Status != "success" || len(page.Data) != 1 {
		t.Fatalf("got %d %+v, want 200 success with 1 row", rec.Code, page)
	}
	if page.Pagination == nil || page.Pagination.Page != 2 || page.Pagination.Limit != 50 || page.Pagination.Total != 120 {
		t.Errorf("pagination = %+v, want 2/50/120", page.Pagination)
	}

	c2, rec2 := testContext(http.MethodPost, "/api/v1/vehicles", "", nil)
	respondCreated(c2, map[string]int64{"id": 7})
	if rec2.Code != http.StatusCreated {
		t.Errorf("respondCreated wrote %d, want 201", rec2.Code)
	}
}
