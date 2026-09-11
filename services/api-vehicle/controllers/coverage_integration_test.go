package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ajb_gps/api-vehicle/models"
	"ajb_gps/internal"
	"ajb_gps/internal/tenant"

	"github.com/DATA-DOG/go-sqlmock"
)

// newTestRedis real untuk healthz (redis:ok). Skip bila Redis tidak tersedia
// di environment (mis. CI tanpa infra) supaya test tidak gagal palsu.
func newTestRedis(t *testing.T) *internal.RedisClient {
	t.Helper()
	cfg := &internal.Config{}
	cfg.Redis.Addr = "127.0.0.1:6390"
	cfg.Redis.DB = 0
	cfg.Redis.PoolSize = 5
	rc, err := internal.NewRedisClient(cfg, nil, nil)
	if err != nil {
		t.Skipf("redis tidak tersedia di %s: %v", cfg.Redis.Addr, err)
	}
	t.Cleanup(func() { rc.Close() })
	return rc
}

// TestIntegration_RouterAndMiddleware exercises the full router, middleware
// chain (CORS, metrics, security headers, rate limiting), and healthz handler.
func TestIntegration_RouterAndMiddleware(t *testing.T) {
	cfg := newTestCfg()
	if cfg.RateLimit.LoginMaxAttempts == 0 {
		cfg.RateLimit.LoginMaxAttempts = 5
		cfg.RateLimit.LoginWindow = 15 * time.Minute
		cfg.RateLimit.APIMaxPerMinute = 100
	}

	tm := &tenant.Manager{}
	metricsH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rc := newTestRedis(t)
	Init(cfg, rc, tm, metricsH)
	router := Router()

	// 1. /healthz — exercises Router, setupRouter, corsMiddleware,
	// httpMetricsMiddleware, securityHeadersMiddleware.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/healthz", nil)
	req.Header.Set("Origin", "http://localhost")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Content-Type-Options") == "" {
		t.Error("expected X-Content-Type-Options security header")
	}

	// 2. Protected route without token — exercises requireAuth (deny path).
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/v1/vehicles", nil)
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("no-auth: expected 401, got %d", w2.Code)
	}

	// 3. CORS preflight — exercises corsMiddleware OPTIONS branch.
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("OPTIONS", "/api/v1/vehicles", nil)
	req3.Header.Set("Origin", "http://localhost")
	req3.Header.Set("Access-Control-Request-Method", "GET")
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNoContent {
		t.Errorf("cors preflight: expected 204, got %d", w3.Code)
	}
	if w3.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Access-Control-Allow-Methods header on preflight")
	}
}

// TestIntegration_AuthorizeAndRBAC exercises authorize + installAuthContext
// by issuing a real JWT and stubbing the company DB for loadCompanyAccess.
func TestIntegration_AuthorizeAndRBAC(t *testing.T) {
	cfg := newTestCfg()
	if cfg.RateLimit.LoginMaxAttempts == 0 {
		cfg.RateLimit.LoginMaxAttempts = 5
		cfg.RateLimit.LoginWindow = 15 * time.Minute
		cfg.RateLimit.APIMaxPerMinute = 100
	}

	tm := &tenant.Manager{}
	metricsH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	Init(cfg, nil, tm, metricsH)
	router := Router()

	mu := models.MasterUser{
		ID: 42, CompanyCode: "DEV001", Email: "t@e.com", Role: "Admin", Status: "active",
	}
	token, _, err := signTokenClaims(mu, "Admin", nil)
	if err != nil {
		t.Fatalf("signTokenClaims: %v", err)
	}

	db, mock := mockDB(t)
	defer db.Close()

	// authorize → loadCompanyAccess query on company DB.
	mock.ExpectQuery("SELECT id, user_id, role_override, is_active FROM user_company_access").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "role_override", "is_active"}).AddRow(1, 42, "", true))

	stubDBByCode(t, db, nil)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/vehicles", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	// Auth must pass (not 401/403). The handler may fail later on the vehicle
	// query, but authorize + installAuthContext were exercised.
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusForbidden {
		t.Errorf("expected auth to pass, got %d body=%s", w.Code, w.Body.String())
	}
}
